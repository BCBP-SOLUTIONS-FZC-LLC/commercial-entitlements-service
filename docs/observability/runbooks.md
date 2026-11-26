# Runbooks: commercial-entitlements alerts

Runbooks for every alert in [`deploy/monitoring/app-alerts.yml`](../../deploy/monitoring/app-alerts.yml), [`deploy/monitoring/slo-rules.yml`](../../deploy/monitoring/slo-rules.yml), and the Helm [`PrometheusRule`](../../deploy/helm/templates/prometheusrule.yaml).

Common starting points:

- Rollouts: `count by (version) (platform_build_info{service="commercial-entitlements",environment="$environment"})`
- Logs (Loki): `{service="commercial-entitlements"} | json`, then follow `trace_id` into Tempo.
- Readiness: `GET /readyz` reports Postgres (app pool + sysPool), Valkey, and the outbox runner.

---

## Availability

### CommercialEntitlementsDown

**Meaning.** No pod has been scraped as healthy for 2 minutes.

**Triage.**
1. `kubectl get pods -l app.kubernetes.io/name=commercial-entitlements` and `kubectl describe pod` for crash loops and probe failures.
2. Startup panics are deliberate for bad config: a missing `MIGRATION_DATABASE_URL` with `PG_BOUNCER_MODE=true`, an invalid `APP_ENV`, a queue without a RedrivePolicy, an unregistered Glue schema definition.
3. Migrations run via the pre-upgrade Helm hook Job; check the migrate Job logs.

**Mitigate.** Roll back the last deploy, or fix the named configuration.

### CommercialEntitlementsReplicasMissing

**Meaning.** Fewer than 2 replicas are healthy (HPA `minReplicas: 2`). One more disruption takes the service to zero.

**Triage.** Same as `CommercialEntitlementsDown`, per pod. Check node pressure and pending pods.

---

## HTTP errors and latency

### CommercialEntitlementsHighErrorRate / CommercialEntitlementsCriticalErrorRate

**Meaning.** More than 5% (warning) or 20% (critical) of requests returned 5xx over 5 minutes.

**Triage.**
1. Which routes: `sum by (route, error_class) (rate(platform_http_requests_total{service="commercial-entitlements",status_class="5xx"}[5m]))`.
2. The error body's `code`:
   - `db_unavailable`: Postgres / PgBouncer. Check `platform_db_pool_connections` and pool acquire errors.
   - `request_timeout`: 30s deadline or `PG_STATEMENT_TIMEOUT`. Check slow-query logs.
   - `cache_unavailable`: Valkey is down. CACHE-1 — service degrades gracefully from Postgres.
   - `internal_error`: unhandled error, logged as `unhandled 500 error`.

**Mitigate.** Roll back a bad version, or restore the failing dependency.

### CommercialEntitlementsHighLatency

**Meaning.** p99 across all routes above 2s.

**Triage.** `histogram_quantile(0.99, sum by (le, route) (rate(platform_http_request_duration_seconds_bucket{service="commercial-entitlements"}[5m])))` to find the route. Then check slow queries and pool saturation. See [`../runbook-recompute-latency.md`](../runbook-recompute-latency.md).

---

## Outbox

### CommercialEntitlementsOutboxDeadLetters

**Meaning.** One or more `TenantEntitlementsChanged` events have exhausted all publish retries and been dead-lettered.

**Triage.** See [`../runbook-outbox-dead-letter.md`](../runbook-outbox-dead-letter.md).

### CommercialEntitlementsOutboxBacklog

**Meaning.** The oldest unpublished outbox event is > 5 minutes old — SNS is failing transiently or the runner cannot keep up.

**Triage.**
1. Check `platform_outbox_pending_events` and `platform_outbox_oldest_pending_age_seconds`.
2. Check outbox runner logs for SNS publish errors.
3. Verify `SNS_TOPIC_ENTITLEMENTS_ARN` is correct and the IRSA role has `sns:Publish`.

### CommercialEntitlementsDLQBacklog

**Meaning.** Messages are sitting in an inbound queue's DLQ.

**Triage.**
1. Identify the queue from `$labels.queue`.
2. Inspect a DLQ message's `DLQReason`, `OriginalQueue`, `FailedAt` attributes.
3. Fix the root cause, then redrive via the SQS console or `aws sqs send-message`.

---

## Database

### CommercialEntitlementsDBPoolSaturated

**Meaning.** > 90% of pool connections checked out on the busiest replica.

**Triage.**
1. `pool="default"` → API pool. `pool="system"` → BYPASSRLS pool (reconcilers + migration).
2. Check `platform_db_pool_empty_acquires_total` for exhaustion.
3. Check `PG_MAX_CONNS` and RDS max_connections.
4. Scale pods (HPA) or increase `PG_MAX_CONNS` (and RDS max_connections proportionally).

---

## Business

### CommercialEntitlementsRecomputeErrors

**Meaning.** Recompute operations failing at a sustained rate.

**Triage.**
1. `sum by (cause, outcome) (rate(commercial_entitlements_recompute_total{outcome="error"}[5m]))`.
2. Check logs for the specific error (Postgres connectivity, optimistic lock conflicts CONC-1, plan catalog unavailable).
3. If optimistic lock conflicts: check for concurrent recompute triggers on the same tenant (billing + membership events arriving simultaneously).

---

## Compliance

### CommercialEntitlementsRLSCrossTenantAccess

**Meaning.** RLS blocked a cross-tenant data access — a valid tenant GUC tried to read/write another tenant's row. Zero tolerance: any occurrence pages immediately.

**Triage.** See [`../runbook-rls-cross-tenant.md`](../runbook-rls-cross-tenant.md).

---

## SLO burn-rate alerts

### CommercialEntitlementsSLOWriteErrorBudgetFastBurn

**Meaning.** Write-path error rate is 14.4× above the SLO budget (> 1.44% of write requests failing over 1h and 5m). SLO-1: 99.9% non-5xx.

**Triage.** Same as `CommercialEntitlementsCriticalErrorRate`. Escalate immediately.

### CommercialEntitlementsSLOWriteErrorBudgetSlowBurn

**Meaning.** Write-path error rate is 6× above the SLO budget (> 0.6% over 6h and 30m). Sustained degradation.

**Triage.** Same as `CommercialEntitlementsHighErrorRate`. Investigate trending dependency failures.
