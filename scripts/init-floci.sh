#!/usr/bin/env bash
# Floci ready-hook — provisions the full commercial-entitlements messaging topology
# per LLD §7.1, §7.3, plus the Glue Schema Registry (§7.3.1).
# Runs automatically on container start via the volume mount to
# /etc/floci/init/ready.d/.
#
# Creates:
#   5 SNS topics         — commercial-entitlements-events (produced by this service),
#                          billing-events, iam-tenant-events, iam-catalog-events,
#                          iam-membership-events (owned by other services; stand-ins
#                          for their Terraform so local dev receives inbound events)
#   4 inbound SQS        — billing-entitlements-q, tenant-entitlements-q,
#                          catalog-entitlements-q, membership-entitlements-q
#                          (+ DLQs, maxReceiveCount=5)
#   SNS→SQS subs         — with EventType filter policies matching each consumer's
#                          case statement in internal/adapter/inbound/consumer/
#   1 Glue registry      — commercial-entitlements-events
#   1 Glue schema        — TenantEntitlementsChanged (registered from the same
#                          JSON Schema Draft-07 file eventbus.GlueCodec ships)
#   Fan-out queues        — downstream consumers of commercial-entitlements-events
#
# The docker-compose `floci` service mounts this repo's
# internal/adapter/outbound/eventbus/schemas/ directory read-only at
# /etc/floci/init/schemas so this script reads schema definitions straight
# from source — one place to update when a schema changes.

set -euo pipefail

AWS_REGION=ap-south-1
AWS_ACCOUNT=000000000000
MAX_RECEIVES=5
SCHEMAS_DIR=/etc/floci/init/schemas

export AWS_DEFAULT_REGION="$AWS_REGION"
export AWS_REGION="$AWS_REGION"

# ── Helpers ──────────────────────────────────────────────────────────────────

topic_arn() { printf 'arn:aws:sns:%s:%s:%s' "$AWS_REGION" "$AWS_ACCOUNT" "$1"; }
queue_arn() { printf 'arn:aws:sqs:%s:%s:%s' "$AWS_REGION" "$AWS_ACCOUNT" "$1"; }

# create_queue_with_dlq <base-queue-name>
# Creates <base>-dlq then <base> with RedrivePolicy → <base>-dlq (maxReceiveCount=5).
create_queue_with_dlq() {
  local queue="$1"
  local dlq="${queue}-dlq"

  aws sqs create-queue --queue-name "$dlq" >/dev/null
  local dlq_arn
  dlq_arn=$(queue_arn "$dlq")

  local attrs
  attrs=$(mktemp)
  cat > "$attrs" <<EOF
{
  "RedrivePolicy": "{\"deadLetterTargetArn\":\"${dlq_arn}\",\"maxReceiveCount\":\"${MAX_RECEIVES}\"}"
}
EOF
  aws sqs create-queue --queue-name "$queue" --attributes "file://$attrs" >/dev/null
  rm -f "$attrs"
}

# subscribe_queue <topic-name> <queue-name> [filter-policy-json]
# Creates an SNS→SQS subscription and applies a filter policy on the
# `EventType` MessageAttribute if provided.
subscribe_queue() {
  local topic="$1" queue="$2" filter="${3:-}"
  local t_arn q_arn
  t_arn=$(topic_arn "$topic")
  q_arn=$(queue_arn "$queue")

  local sub_arn
  sub_arn=$(aws sns subscribe \
    --topic-arn "$t_arn" \
    --protocol sqs \
    --notification-endpoint "$q_arn" \
    --attributes RawMessageDelivery=true \
    --query 'SubscriptionArn' --output text)

  if [ -n "$filter" ]; then
    local attrs
    attrs=$(mktemp)
    printf '%s' "$filter" > "$attrs"
    aws sns set-subscription-attributes \
      --subscription-arn "$sub_arn" \
      --attribute-name FilterPolicy \
      --attribute-value "file://$attrs" >/dev/null
    rm -f "$attrs"
  fi
}

# register_schema <registry> <schema-file> <schema-name>
# Idempotently creates the registry and registers schema-file's contents.
# DataFormat=JSON, Compatibility=BACKWARD mirrors schema-gov register defaults.
register_schema() {
  local registry="$1" file="$2" name="$3"
  aws glue create-schema \
    --registry-id "RegistryName=${registry}" \
    --schema-name "$name" \
    --data-format JSON \
    --compatibility BACKWARD \
    --schema-definition "file://${SCHEMAS_DIR}/${file}" >/dev/null
}

# ── SNS topics ────────────────────────────────────────────────────────────────

# Produced by this service.
aws sns create-topic --name commercial-entitlements-events >/dev/null

# Owned by other services; created here so inbound subscriptions work in local dev.
aws sns create-topic --name billing-events         >/dev/null
aws sns create-topic --name iam-tenant-events      >/dev/null
aws sns create-topic --name iam-catalog-events     >/dev/null
aws sns create-topic --name iam-membership-events  >/dev/null

# ── Inbound SQS — this service reads these (LLD §7.1) ────────────────────────

# billing-entitlements-q ← billing-events
# Handles: TenantPlanChanged (plan upgrade/downgrade → recompute),
#          TenantSubscriptionCancelled (subscription end → effective set update),
#          TenantReactivated (resubscription → recompute),
#          TenantQuotaAddOnChanged (addon purchase/refund — E-1 / BL-OQ-4, C-5 / BL-OQ-20).
# See: internal/adapter/inbound/consumer/billing_consumer.go
create_queue_with_dlq billing-entitlements-q
subscribe_queue billing-events billing-entitlements-q \
  '{"EventType": ["TenantPlanChanged","TenantSubscriptionCancelled","TenantReactivated","TenantQuotaAddOnChanged"]}'

# tenant-entitlements-q ← iam-tenant-events
# Handles: TenantIdentityProvisioned (new tenant provisioned via RP → bootstrap snapshot),
#          TenantSubscriptionUpdated (subscription change → recompute),
#          TenantMembershipsPurged (all members removed → purge trial / clear snapshot),
#          TenantConverted (trial → paid conversion → update snapshot),
#          TenantOffboarded (offboarding → purge entitlement data).
# See: internal/adapter/inbound/consumer/tenant_consumer.go
create_queue_with_dlq tenant-entitlements-q
subscribe_queue iam-tenant-events tenant-entitlements-q \
  '{"EventType": ["TenantIdentityProvisioned","TenantSubscriptionUpdated","TenantMembershipsPurged","TenantConverted","TenantOffboarded"]}'

# catalog-entitlements-q ← iam-catalog-events
# Handles: PlanCatalogChanged (plan definition updated → invalidate ent:plans cache,
#          trigger recompute for affected tenants).
# See: internal/adapter/inbound/consumer/catalog_consumer.go
create_queue_with_dlq catalog-entitlements-q
subscribe_queue iam-catalog-events catalog-entitlements-q \
  '{"EventType": ["PlanCatalogChanged"]}'

# membership-entitlements-q ← iam-membership-events
# Handles: TenantMembershipsPurged (tenant hard-delete, all members removed → purge),
#          TenantStateChanged (tenant state transition: suspended/cancelled/active → recompute).
# See: internal/adapter/inbound/consumer/membership_consumer.go
create_queue_with_dlq membership-entitlements-q
subscribe_queue iam-membership-events membership-entitlements-q \
  '{"EventType": ["TenantMembershipsPurged","TenantStateChanged"]}'

# ── Downstream fan-out of commercial-entitlements-events ─────────────────────
# In real deployments these belong to the consumer services; provisioning them
# here lets local dev observe the full fan-out of TenantEntitlementsChanged.

# Billing Service — enforces quota limits against the new effective set.
create_queue_with_dlq billing-entl-fanout-q
subscribe_queue commercial-entitlements-events billing-entl-fanout-q

# Audit Log — catch-all for compliance logging of entitlement changes.
create_queue_with_dlq audit-entl-fanout-q
subscribe_queue commercial-entitlements-events audit-entl-fanout-q

# ── Glue Schema Registry (LLD §7.3.1) ────────────────────────────────────────
# One registry matching the single SNS topic. Populated from the JSON Schema
# Draft-07 file that eventbus.GlueCodec embeds so local dev exercises the real
# Glue wire format (18-byte header prefix on every published envelope).

aws glue create-registry --registry-name commercial-entitlements-events >/dev/null

# TenantEntitlementsChanged — the one event this service produces.
register_schema commercial-entitlements-events \
  tenant_entitlements_changed.json \
  TenantEntitlementsChanged

# ── Summary ───────────────────────────────────────────────────────────────────

topic_count=$(aws sns list-topics --query 'length(Topics)' --output text)
queue_count=$(aws sqs list-queues --query 'length(QueueUrls)' --output text)
sub_count=$(aws sns list-subscriptions --query 'length(Subscriptions)' --output text)
schema_count=$(aws glue list-schemas \
  --registry-id RegistryName=commercial-entitlements-events \
  --query 'length(Schemas)' --output text)

echo "Floci init complete."
echo "  SNS topics:        ${topic_count} (expected 5)"
echo "  SQS queues+DLQs:   ${queue_count} (expected 12 = 6 pairs)"
echo "  SNS subscriptions: ${sub_count}  (expected 6)"
echo "  Glue schemas:      ${schema_count}  (expected 1 in commercial-entitlements-events)"
