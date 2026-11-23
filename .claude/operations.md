# Operations

## Security

### Tenant Isolation — Three Layers

- **Layer 1** — Kubernetes NetworkPolicy: only `commercial` namespace service accounts reach `/internal/v1/`; no public internet exposure.
- **Layer 2** — PostgreSQL RLS: `app.tenant_id` GUC, `ENABLE + FORCE ROW LEVEL SECURITY`. GUC bound **transaction-locally** (`SET LOCAL`) by `pgcommon.GUCSetFromContext` on every pool checkout. Never session-scoped (RLS-6).
- **Layer 3** — Audit-tagged audit log via platform-audit outbox mode.

### Input Validation

- `tenant_id` on path and body: validated as `uuid.UUID` at handler layer.
- `limit_key`: validated against `domain.KnownLimitKeys` map (`validateLimitKey` in `service/helpers.go`).
- `value`: validated as non-negative `int64` for overrides and addon grants.
- `expires_at`: validated as future timestamp when provided.
- `profile` (trial): validated against `domain.TrialProfile` ENUM.
- `cause` (recompute): validated against `domain.EffectiveSetCause` ENUM.

### Authorization Matrix

| Action | Required | Notes |
|--------|----------|-------|
| Read effective set | Calling service (in-mesh mTLS) | No JWT; NetworkPolicy enforced |
| Trigger recompute | Calling service (in-mesh mTLS) | |
| Set/remove overrides | Operator tooling (in-mesh) | Recorded in override_history |
| Grant/revoke addon | Billing service (in-mesh) | |
| Record drawdown | Quota enforcer (in-mesh) | Idempotent via request_id |
| Resolve shortfall | Operator tooling (in-mesh) | |
| Set trial assignment | IAM/billing (in-mesh) | |
| Catalog refresh | Internal cron / calling service | |

## Observability

### Prometheus Metrics (`internal/adapter/outbound/metrics/`)

Namespace: `commercial_entitlements_*`. Registered via `metrics.Register(appEnv)` onto `gincommon.MetricsRegisterer()`. Registry is guarded by `sync.Once` — safe to call from both `cmd/server` and `cmd/reconciler`.

All metrics are Tier 3 service-specific (`commercial_entitlements_*`). Required labels `service`, `domain`, and `environment` are injected centrally by `gincommon.MetricsRegisterer()` as const labels (Rule 8). Tier 1 platform metrics (`platform_*`) are registered automatically by `events.InitMetrics()`, `pgmetrics.InitWithIdentity()`, and `audit.InitMetrics()`.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `commercial_entitlements_recompute_total` | Counter | `cause`, `outcome` | Total recomputations; `outcome` ∈ `computed`\|`noop`\|`error` (`noop` = ENT-INV-6 skipped) |
| `commercial_entitlements_recompute_duration_seconds` | Histogram | `cause`, `outcome` | Wall-clock time per Recompute call |
| `commercial_entitlements_override_set_total` | Counter | `limit_key` | Override sets |
| `commercial_entitlements_override_remove_total` | Counter | `limit_key` | Override removes |
| `commercial_entitlements_override_expire_total` | Counter | — | Overrides swept by reconciler |
| `commercial_entitlements_addon_grant_total` | Counter | `limit_key` | Addon packs granted |
| `commercial_entitlements_addon_expire_total` | Counter | — | Addons swept by reconciler |
| `commercial_entitlements_drawdown_total` | Counter | `limit_key`, `shortfall` | Drawdown requests; `shortfall` ∈ `yes`\|`no` |
| `commercial_entitlements_shortfall_raised_total` | Counter | `limit_key`, `status` | Shortfalls raised; `status` ∈ `auto_accepted`\|`pending_approval` |
| `commercial_entitlements_shortfall_resolve_total` | Counter | `disposition` | Shortfalls resolved by operators; `disposition` ∈ `approve`\|`reject` |
| `commercial_entitlements_cache_hit_total` | Counter | `kind` | Valkey advisory cache hits |
| `commercial_entitlements_cache_miss_total` | Counter | `kind` | Valkey advisory cache misses (fell through to Postgres/IAM) |
| `commercial_entitlements_duplicate_messages_total` | Counter | `consumer`, `event_type` | Duplicate events detected and skipped by inbox dedup layer (Tier-3 complement to Tier-1 `platform_duplicate_messages_total`) |
| `commercial_entitlements_unknown_event_acknowledged_total` | Counter | `event_type` | Events with no registered handler acknowledged for forward compatibility |

**Label cardinality:** no `tenant_id`/`user_id`/`request_id`/`event_id`/`session_id` label anywhere (prohibited by standard).

**`kind` values for cache metrics:** `effective_set`, `plan`.

**`cause` values for recompute metrics:** `plan_change`, `override_change`, `addon_change`, `trial_change`, `catalog_refresh`, `billing_event`, `manual_recompute`, `tenant_lifecycle`, `shortfall_resolution`.

### Prometheus Pushgateway (reconciler)

`cmd/reconciler/main.go` pushes metrics to `PROMETHEUS_PUSHGATEWAY_URL` after each CronJob run if the env var is set. Groups by `cronjob=<jobName>`.

### SLOs

| Endpoint | p99 target |
|----------|-----------|
| `GET /entitlements` (cache hit) | 10 ms |
| `GET /entitlements` (cache miss) | 30 ms |
| `POST /entitlements/recompute` | 100 ms |
| `POST /addons/drawdown` | 50 ms |
| Outbound event publish half | 1 s |
| Inbound event → snapshot fresh | 30 s |

### Tracing

`gincommon.InitTracingWithConfig` wires OTel. `port.SetTraceExtractor` captures `TraceID` for log correlation. `port.SetConsumerSpanFunc` wraps each SQS handler call in a child span linked to the event's upstream span. IAM client uses `otelhttp.NewTransport` for W3C `traceparent` injection (OBS-9).

## Reconciler Jobs

Dispatched by `cmd/reconciler --job=<name>` as Kubernetes CronJobs (`deploy/helm/templates/cronjobs.yaml`).

| Job name | CronJob | Purpose | Key dep |
|----------|---------|---------|---------|
| `override-expiry` | `commercial-entitlements-override-expiry` | Sweep `entitlement_overrides` where `expires_at <= now`, mark `deleted_at`, write override_history | Postgres (sysPool) |
| `addon-expiry` | `commercial-entitlements-addon-expiry` | Sweep `quota_addons` where `status='active'` and `expires_at <= now`, mark `status='expired'` | Postgres (sysPool) |
| `outbox-prune` | `commercial-entitlements-outbox-prune` | Delete `outbox_events` rows older than `OUTBOX_RETENTION_DAYS` (default 8 days) | Postgres (sysPool) |
| `processed-events-prune` | `commercial-entitlements-processed-events-prune` | Delete `processed_events` rows older than `PROCESSED_EVENTS_TTL_DAYS` (default 8 days) | Postgres (sysPool) |
| `quota-reset` | `commercial-entitlements-quota-reset` | SCAN+DEL Valkey `quota:usage:*:{key}:{prev_period}` at billing period rollover (ENT-D13) | Valkey (`VALKEY_URL`) |

All reconciler jobs use the **system pool** (`SYSTEM_DATABASE_URL`, `BYPASSRLS`) for cross-tenant sweeps. `SYSTEM_DATABASE_URL` is required in non-dev environments.

**Reconciler env vars:**
- `RECONCILER_JOB` — job name (or use `--job=` flag)
- `RECONCILER_TIMEOUT` — max wall time (default 5m)
- `RECONCILER_BATCH_LIMIT` — rows per sweep (default 500)
- `RECONCILER_STATEMENT_TIMEOUT` — Postgres statement timeout (default 2m)
- `OUTBOX_RETENTION_DAYS` — outbox retention (default 8)
- `PROCESSED_EVENTS_TTL_DAYS` — inbox retention (default 8)

## Deployment (`deploy/helm/`)

**Binaries per Helm release:**
- `Deployment` — `cmd/server` (HTTP + SQS + outbox)
- `Job` (pre-upgrade hook) — `cmd/migrate` (runs `postgres.MigrateSchema` once per release)
- 5 `CronJob`s — `cmd/reconciler --job=<name>` (see table above)

**Schema migration:**
- `Deployment` sets `RUN_MIGRATIONS=false` to skip auto-migration in replicas.
- The `migrate-job.yaml` pre-upgrade hook runs `cmd/migrate` exactly once per Helm release.
- `MIGRATION_DATABASE_URL` is a **direct Postgres DSN**, bypassing PgBouncer (DDL requires direct connection — CONFIG-2).

**Health gates:**
- `/readyz` requires Postgres healthy, Valkey reachable, and outbox runner ready.
- `/healthz` additionally requires the audit runner alive (if configured).

**Two pools:**
- **App pool** — `DATABASE_URL` via PgBouncer transaction pooling; RLS enforced; `BYPASSRLS = false`.
- **System pool** — `SYSTEM_DATABASE_URL` direct Postgres; `BYPASSRLS = true`; used by reconciler sweeps.

## Configuration

See `.env-example` for the full list. Key env vars:

| Var | Default | Notes |
|-----|---------|-------|
| `DATABASE_URL` | — | App pool DSN (PgBouncer) |
| `MIGRATION_DATABASE_URL` | — | Direct Postgres for migrations |
| `SYSTEM_DATABASE_URL` | — | BYPASSRLS pool for reconciler; required in non-dev |
| `VALKEY_URL` | `localhost:6379` | Valkey advisory cache |
| `IAM_CATALOG_BASE_URL` | — | IAM plan catalog endpoint |
| `IAM_CATALOG_API_KEY` | — | API key for IAM catalog |
| `TEMPORAL_HOST_PORT` | — | Temporal; optional — skipped if unset |
| `SNS_TOPIC_ENTITLEMENTS_ARN` | — | SNS topic for produced events |
| `SQS_BILLING_ENTITLEMENTS_QUEUE_URL` | — | Billing events queue |
| `SQS_TENANT_ENTITLEMENTS_QUEUE_URL` | — | Tenant lifecycle queue |
| `SQS_CATALOG_ENTITLEMENTS_QUEUE_URL` | — | Catalog changes queue |
| `SQS_MEMBERSHIP_ENTITLEMENTS_QUEUE_URL` | — | Membership changes queue |
| `GLUE_REGISTRY_ENTITLEMENTS_NAME` | — | Glue schema registry for event schemas |
| `RUN_MIGRATIONS` | `true` | Set `false` in Kubernetes replicas |
| `DOCS_ENABLED` | `false` | Swagger UI; requires `DOCS_AUTH_TOKEN` in staging/prod |
