#!/usr/bin/env bash
# Registers produced event schemas with AWS Glue Schema Registry.
# commercial-entitlements produces one event type:
#   tenant_entitlements_changed
#
# Each schema JSON is registered (or updated) in the registry named by
# GLUE_REGISTRY_ENTITLEMENTS_NAME. The schema version is determined by
# the Glue API (auto-increment on content change). A no-op registration
# (identical content) is silently skipped by Glue.
set -euo pipefail

: "${GLUE_REGISTRY_ENTITLEMENTS_NAME:?GLUE_REGISTRY_ENTITLEMENTS_NAME is required}"
: "${AWS_REGION:?AWS_REGION is required}"

SCHEMA_DIR="internal/adapter/outbound/eventbus/schemas"

register_schema() {
  local registry="$1"
  local name="$2"
  local file="$3"

  echo "  Registering ${name} in ${registry}..."
  aws glue register-schema-version \
    --schema-id "RegistryName=${registry},SchemaName=${name}" \
    --schema-definition "file://${file}" \
    --region "${AWS_REGION}" \
    --output json \
  | jq -r '"    version: " + (.VersionNumber | tostring) + "  status: " + .Status'
}

echo "::group::Register entitlements event schemas (registry: ${GLUE_REGISTRY_ENTITLEMENTS_NAME})"
register_schema \
  "${GLUE_REGISTRY_ENTITLEMENTS_NAME}" \
  "tenant_entitlements_changed" \
  "${SCHEMA_DIR}/tenant_entitlements_changed.json"
echo "::endgroup::"

echo "  ✔  all entitlements event schemas registered"
