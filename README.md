# commercial-entitlements

Go service for the XpertPMS Commercial domain. Resolves each tenant's **effective entitlement set** from subscription plans, manual overrides, trial assignments, and quota addon packs. Publishes `tenant_entitlements_changed` after every recomputation and enforces quota limits via a drawdown + shortfall workflow.

**Repository:** `github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements`
**Module:** Go — private module — deployed as six binaries from one image (`cmd/server`, `cmd/reconciler`, `cmd/worker`, `cmd/migrate`, `cmd/expiry`, `cmd/catalog-refresh`)
**Design:** LLD `docs/lld/commercial-lld-entitlements-service.md` — where LLD and HLD disagree, HLD is authoritative.

---

## Mental model

| This service owns | It does NOT own |
|---|---|
| `entitlement_snapshots` — versioned effective limit sets (plan + overrides + addons + trial) per tenant | The plan definition catalog itself (IAM Org Membership / Catalog Admin Config) |
| `entitlement_overrides` — operator-set per-tenant, per-limit-key value overrides with optional expiry | Subscription lifecycle, seat counts, feature-flag deltas (IAM Org Membership) |
| `quota_addons` — quota addon packs with per-pack remaining tokens and FIFO drawdown | Raw usage event ingestion (Usage & Metering) |
| `quota_addon_drawdowns` — idempotent drawdown records (ENT-INV-3) | Billing plan changes / payment events (Billing Service) |
| `quota_shortfalls` — raised when packs are exhausted; operator resolves approve/reject | Tenant provisioning, realm management (Realm Provisioner) |
| `tenant_trial_assignments` / `trial_entitlement_overrides` — trial profile bundles applied only while `state == trial` | Display identity, membership roles (IAM Org Membership) |
| `plan_definitions` (Postgres cache + Valkey) — read-through over IAM plan catalog | Workflow execution or tender ACL (Workflow Service / Tender ACL) |
| Quota ceiling keys in Valkey (`quota:tenant:{id}:{key}`) — sub-millisecond quota gate for the enforcer | Audit delivery of quota events (Audit Log Service subscribes to `tenant_entitlements_changed`) |

This service is **internal-only** — no public internet exposure. All routes are in-mesh mTLS only.

---

## Why this service exists

Every quota-gated request in the platform needs one authoritative answer to "what are this tenant's current limits and how much quota remains?" Centralising that projection behind one service means:

- The quota enforcer (`ENT-I3`) has exactly one place to deduct from, with one idempotency contract (`request_id`), instead of re-reading addons and overrides per request.
- Plan overrides, trial overrides, addon packs, and manual overrides are merged in one deterministic 5-step pipeline (Recompute) and published atomically — downstream consumers never see partial updates.
- Trial profiles are enforced at the merge step (ENT-INV-5) so trial limits automatically disappear on conversion, without any downstream service needing to track trial state.
- The catalog read-through (Valkey → Postgres → IAM) means a plan-definition change fans out to all affected tenants exactly once via Temporal, rather than each service re-fetching catalog definitions independently.

---

## Quick start

```bash
make setup        # copy .env-example → .env  (first time only)
make docker-up    # start Postgres, Valkey, Temporal, LocalStack
make migrate      # apply pending schema migrations
make run          # run cmd/server on :8080 (metrics on :9090)
make test-unit    # unit tests — no Docker needed
make test         # full test suite — requires Docker
make ci           # tidy + fmt-check + vet + lint + arch-lint + check-metrics + test-ci + build
```

See `Makefile` for the complete target list (`make help`).

---

## What this service does

| Capability | Description |
|---|---|
| Effective set | Merges plan limits + trial overrides + manual overrides + addon balances into a single snapshot; published as `tenant_entitlements_changed` |
| Quota enforcement | Records drawdowns against addon packs (ENT-I3); raises shortfalls when packs are exhausted; operator resolves via approve/reject |
| Overrides | Operator-set per-tenant, per-limit-key value overrides with optional expiry |
| Trial profiles | Plan-override bundles applied only while `state == trial`; purged on offboarding |
| Catalog | Read-through over IAM plan definitions (Postgres cache + Valkey; Temporal fan-out on change) |
| Advisory cache | Valkey caches effective sets and plan definitions; quota ceilings and usage counters for sub-millisecond enforcement |

---

## Binaries

| Binary | Source | Purpose |
|--------|--------|---------|
| `server` | `cmd/server` | HTTP API + 4 SQS consumers + transactional outbox runner + audit runner |
| `reconciler` | `cmd/reconciler` | CronJob dispatcher (`--job=<name>`) for expiry, pruning, and quota-reset |
| `worker` | `cmd/worker` | Temporal workflow worker for catalog fan-out |
| `migrate` | `cmd/migrate` | Schema migration (Helm pre-upgrade Job) |
| `expiry` | `cmd/expiry` | One-shot addon/override expiry sweep (can run standalone) |
| `catalog-refresh` | `cmd/catalog-refresh` | Pull IAM plan catalog into Postgres + Valkey cache |

---

## Architecture

Hexagonal (Ports & Adapters) — dependencies point inward; `internal/core/domain` has no external dependencies.

```
cmd/server        ← composition root: wires HTTP + SQS consumers + outbox + audit
internal/
  core/
    domain/       ← entities, value objects, errors (no external deps)
    port/         ← inbound and outbound interfaces
    service/      ← use-case implementations
  adapter/
    inbound/
      http/       ← Gin handlers and DTOs
      consumer/   ← SQS event consumers (billing, tenant, catalog, membership)
      sqspipeline/ ← platform-events SQS consumer wiring
    outbound/
      postgres/   ← repository implementations (pgcommon)
      valkey/     ← advisory cache (go-redis)
      eventbus/   ← transactional outbox publisher, Glue codec
      iamclient/  ← IAM plan catalog + tenant subscription HTTP client
      auditrecorder/ ← audit log via platform-audit outbox
      metrics/    ← Prometheus Tier-3 collectors (gincommon)
  temporal/       ← catalog fan-out workflow + activity + TemporalStarter adapter
```

Layer boundary rules are enforced at CI time by `make arch-lint` (`.go-arch-lint.yml`).

Full package tree and architectural decisions: `.claude/architecture.md`

### Dependency rules (enforced by `go-arch-lint` in CI)

| Component | May depend on |
|---|---|
| `core_domain` | nothing |
| `core_port` | `core_domain` |
| `core_service` | `core_domain`, `core_port` |
| `adapters_inbound_http` | `core_domain`, `core_port` |
| `adapters_inbound_consumer` | `core_domain`, `core_port`, `adapters_outbound_metrics` |
| `adapters_inbound_sqspipeline` | `core_port` |
| `adapters_outbound_postgres/valkey/eventbus/iamclient/auditrecorder` | `core_domain`, `core_port` |
| `adapters_outbound_metrics` | `core_domain` |
| `adapters_temporal` | `core_domain`, `core_port`, `core_service` |
| `cmd` | everything except cross-adapter imports |

---

## Infrastructure dependencies

| Service | Port | Purpose |
|---------|------|---------|
| PostgreSQL 16 | 5432 | State, RLS, transactional outbox (`outbox_events`), audit (`pending_audit_entries`) |
| Valkey 7 | 6379 | Advisory cache — effective sets, plan definitions, quota ceilings, usage counters |
| Temporal | 7233 | Optional — catalog fan-out workflow; skipped when `TEMPORAL_HOST_PORT` is unset |
| LocalStack / AWS | 4566 | SNS (produced events), SQS (consumed events), Glue schema registry |

---

## API surface

Three route groups, all under `/api/v1/`. All routes are **internal-only** (in-mesh mTLS + NetworkPolicy). No JWT parsing — calling services are authenticated by the mesh.

### Internal routes — `/api/v1/internal/tenants/:id/`

Called by in-mesh services that need raw entitlement data or quota enforcement.

| Method & Path | Ref | Purpose |
|---|---|---|
| `GET /api/v1/internal/tenants/:id/entitlements` | ENT-I1 | Get latest effective set (cache → snapshot; no recompute) |
| `POST /api/v1/internal/tenants/:id/entitlements/recompute` | ENT-I1b | Trigger full 5-step recompute; body `{cause}` |
| `GET /api/v1/internal/tenants/:id/plans/current` | ENT-I2 | Get the tenant's current plan definition |
| `POST /api/v1/internal/tenants/:id/entitlements/drawdown` | ENT-I3 | Record a quota drawdown (idempotent via `request_id`) |

### Tenant routes — `/api/v1/tenants/:id/`

JWT-scoped — tenant sees only its own entitlements.

| Method & Path | Ref | Purpose |
|---|---|---|
| `GET /api/v1/tenants/:id/entitlements` | ENT-T1 | Get latest effective set (tenant-scoped JWT) |

### Operator routes — `/api/v1/operator/tenants/:id/`

Operator tooling (in-mesh mTLS). All mutations recorded in `override_history`.

| Method & Path | Ref | Purpose |
|---|---|---|
| `GET /entitlements/overrides` | ENT-O1 | List all active overrides for a tenant |
| `GET /entitlements/overrides/:limitKey` | ENT-O1 | Get a single active override |
| `PUT /entitlements/overrides/:limitKey` | ENT-O2 | Set or update an override |
| `DELETE /entitlements/overrides/:limitKey` | ENT-O2 | Remove an override |
| `GET /entitlements/overrides/:limitKey/history` | ENT-O1 | Full override lifecycle history |
| `PUT /entitlements/trial-profile` | ENT-O3 | Assign or update trial profile |
| `GET /entitlements/trial-profile` | ENT-O3 | Get current trial assignment |
| `DELETE /entitlements/trial-profile` | ENT-O3 | Purge all trial data for a tenant |
| `GET /entitlements/trial-overrides` | ENT-O4 | List trial-scoped limit overrides |
| `PUT /entitlements/trial-overrides/:limitKey` | ENT-O4 | Set a trial-scoped override |
| `DELETE /entitlements/trial-overrides/:limitKey` | ENT-O4 | Remove a trial-scoped override |
| `GET /entitlements/addons` | ENT-O5 | List all addon packs |
| `POST /entitlements/addons` | ENT-O5 | Grant a new quota addon pack |
| `DELETE /entitlements/addons/:addonId` | ENT-O5 | Revoke an addon pack |
| `GET /entitlements/addons/drawdowns` | ENT-O5 | List drawdown records |
| `GET /entitlements/addons/shortfalls` | ENT-O6 | List shortfalls (optionally by `status`) |
| `POST /entitlements/addons/shortfalls/:shortfallId/resolve` | ENT-O7 | Approve or reject a pending shortfall |
| `GET /api/v1/operator/plans` | — | List all cached plan codes |
| `POST /api/v1/operator/plans/:planCode/refresh` | — | Trigger manual catalog refresh for a plan |

Full route catalogue and Valkey key patterns: `.claude/api-caching-events.md`
Swagger UI: `GET /swagger/index.html` (requires `DOCS_ENABLED=true`)

### Input validation

| Rule | Enforcement |
|---|---|
| `tenant_id` on path and body | Validated as `uuid.UUID` at handler layer; RLS returns zero rows on any mismatch |
| `limit_key` | Validated against `domain.KnownLimitKeys` map (`validateLimitKey` in `service/helpers.go`) |
| `value` | Validated as non-negative `int64` for overrides and addon grants |
| `expires_at` | Validated as a future timestamp when provided |
| `profile` (trial) | Validated against `domain.TrialProfile` ENUM |
| `cause` (recompute) | Validated against `domain.EffectiveSetCause` ENUM |
| Drawdown `request_id` | Deduped at repository layer (ENT-INV-3) — a duplicate returns the existing drawdown with no double-deduction |

---

## Events

### Produced

| Event type | Schema | Trigger | Topic |
|---|---|---|---|
| `tenant_entitlements_changed` | `internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json` | Every successful `Recompute` that writes a new snapshot version | `SNS_TOPIC_ENTITLEMENTS_ARN` |

Payload: `tenant_id`, `version`, `previous_version`, `cause`, `changed_keys`, `computed_at`. Full schema: `api/asyncapi.yaml` → `TenantEntitlementsChangedPayload`.

### Consumed

| Queue env var | Queue name | Producer | Event types |
|---|---|---|---|
| `SQS_BILLING_ENTITLEMENTS_QUEUE_URL` | `billing-entitlements-q` | Billing Service | `TenantPlanChanged`, `TenantSubscriptionCancelled`, `TenantReactivated` → Recompute; `TenantQuotaAddOnChanged{reason:purchase}` → ApplyBillingPurchase (E-1); `TenantQuotaAddOnChanged{reason:refund}` → ApplyBillingRefund (C-5) |
| `SQS_TENANT_ENTITLEMENTS_QUEUE_URL` | `tenant-entitlements-q` | Realm Provisioner (via `iam.tenant.events`) | `TenantIdentityProvisioned`, `TenantSubscriptionUpdated` → Recompute; `TenantMembershipsPurged`, `TenantConverted`, `TenantOffboarded` → handleOffboard (Recompute + optional PurgeTrial) |
| `SQS_CATALOG_ENTITLEMENTS_QUEUE_URL` | `catalog-entitlements-q` | Catalog / Admin Config Service | `PlanCatalogChanged` → InvalidatePlan cache + Temporal fan-out |
| `SQS_MEMBERSHIP_ENTITLEMENTS_QUEUE_URL` | `membership-entitlements-q` | IAM Org Membership (via `iam.membership.events`) | `TenantMembershipsPurged` → Recompute; `TenantStateChanged{cause:TrialReactivated}` → Recompute (E-3 read-race fix) |

Each queue has a `-dlq` with `maxReceiveCount = 5` RedrivePolicy. `platform-events` forwards at `SQS_MAX_RECEIVE_COUNT` = 4 (one below the RedrivePolicy). Idempotency via `processed_events` / `inbox.Store.Process`.

Event contracts: `api/asyncapi.yaml` · JSON schemas: `internal/adapter/outbound/eventbus/schemas/`

---

## Local development

### Prerequisites

- Go 1.23+
- Docker (Postgres, Valkey, Temporal, LocalStack — `make docker-up`)
- `GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*` and an SSH key registered with the BCBP org

### Setup

```bash
git clone https://github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements
cd commercial-entitlements
make setup       # copies .env-example → .env (run once)
make tidy        # go mod tidy
make docker-up   # start PostgreSQL + Valkey + LocalStack + Temporal
make migrate     # apply schema migrations
make run         # start the server on :8080 (metrics on :9090)
```

### Common commands

| Command | Description |
|---|---|
| `make setup` | Copy `.env-example` → `.env` (if absent) |
| `make tidy` / `make fmt` / `make fmt-check` / `make vet` | Go basics; `fmt-check` mirrors CI, does not modify files |
| `make lint` | `golangci-lint` |
| `make arch-lint` | `go-arch-lint` against `.go-arch-lint.yml` (same script CI runs) |
| `make check-metrics` | Validate metric names, suffixes, labels, tier compliance |
| `make swag` | Regenerate `docs/swagger/` from handler annotations |
| `make test-unit` | Unit tests only, no Docker |
| `make test` | Full test suite (Docker required) |
| `make test-ci` | Same, with `-race` + coverage (what CI runs) |
| `make build` | Compile all 6 binaries to `bin/` |
| `make docker-up` / `make docker-down` | Start/stop local infrastructure |
| `make ci` | Full CI gate: tidy + fmt-check + vet + lint + arch-lint + check-metrics + test-ci + build |

### Calling the API locally

```bash
# After make docker-up and make run:

TENANT_ID=$(python3 -c 'import uuid; print(uuid.uuid4())')

# ENT-I1b: trigger recompute
curl -i -X POST http://localhost:8080/api/v1/internal/tenants/$TENANT_ID/entitlements/recompute \
  -H "Content-Type: application/json" \
  -H "x-tenant-id: $TENANT_ID" \
  -d '{"cause":"manual_recompute"}'

# ENT-I1: get effective set
curl -i http://localhost:8080/api/v1/internal/tenants/$TENANT_ID/entitlements \
  -H "x-tenant-id: $TENANT_ID"

# ENT-I3: record a drawdown (quota enforcer path)
curl -i -X POST http://localhost:8080/api/v1/internal/tenants/$TENANT_ID/entitlements/drawdown \
  -H "Content-Type: application/json" \
  -H "x-tenant-id: $TENANT_ID" \
  -d '{"limit_key":"llm_token_quota_monthly","tokens_consumed":1000,"request_id":"req-abc123","period_start":"2026-11-01T00:00:00Z"}'
```

---

## Testing domain events locally

Every mutating write publishes a domain event through a **transactional outbox → SNS → SQS** pipeline.

```
HTTP write / reconciler job / SQS consumer
    │
    ▼
service layer  ──(same tx)──▶  outbox_events (Postgres)
                                      │
                               outbox runner (OUTBOX_POLL_INTERVAL)
                                      │
                                      ▼
                         SNS: commercial.entitlements.events   (LocalStack)
                                      │
                    SNS fan-out to downstream SQS queues
```

### Step 1 — Start infrastructure

```bash
make docker-up
```

### Step 2 — Trigger an event and inspect the outbox

```bash
make run
# ... issue a recompute or override call (see above) ...

docker compose exec postgres psql -U commercial_entitlements_app -d commercial_entitlements -c \
  "SELECT id, event_type, published_at IS NOT NULL AS published, attempts \
   FROM outbox_events ORDER BY created_at DESC LIMIT 20;"
```

### Troubleshooting events

| Symptom | Likely cause | Fix |
|---|---|---|
| `outbox_events` row never gets `published_at` set | Topic ARN mismatch or outbox runner not started | Re-check `SNS_TOPIC_ENTITLEMENTS_ARN` in `.env` against `aws --endpoint-url http://localhost:4566 sns list-topics` |
| `outbox_events` empty after a write | Row was published and pruned, or the write never committed | Re-check the HTTP response code — a `2xx` guarantees the row was committed |
| `make run` panics with `resolve glue schema ... by definition` | This build's schema definition is not registered in the Glue registry | `make docker-down && make docker-up`, or unset `GLUE_REGISTRY_ENTITLEMENTS_NAME` for plain JSON |
| An inbound event lands in `*-entitlements-q-dlq` immediately | Permanent reject — check `DLQReason` attribute (`schema_violation`) | Fix the payload or `api/asyncapi.yaml` |

---

## Testing

### Coverage

`.github/scripts/coverage-gate.sh` reads `go tool cover -func=coverage.out`'s total and fails below `COVERAGE_THRESHOLD` (default **95%**). Coverage is measured over `./internal/...`, merged across unit/postgres/integration suites. Run `make test-ci` to see the current merged total.

### Running a single test

```bash
go test ./test/unit/... -run TestRecompute_NoopGuard_ENT_INV_6 -v
go test ./test/... -tags=integration -run TestRLS_CrossTenantLeak -v
```

---

## Observability

- **Metrics:** `commercial_entitlements_*` (Tier 3, `gincommon.NewCounterVec` etc.) + `platform_*` (Tier 1, via `events.InitMetrics`, `pgmetrics.InitWithIdentity`, `audit.InitMetrics`). Domain: `commercial`. Scrape: `METRICS_PORT` (default 9090).
- **Tracing:** OTel via `gincommon.InitTracingWithConfig`. W3C `traceparent` propagated to IAM client and SQS consumer spans.
- **Logging:** Structured JSON via `port.Logger` → `platform-gincommon/pkg/logger`.
- **Alerts:** `deploy/monitoring/app-alerts.yml`, `deploy/helm/templates/prometheusrule.yaml`.

**SLOs:**

| Endpoint | p99 target |
|---|---|
| `GET /entitlements` (cache hit) | 10 ms |
| `GET /entitlements` (cache miss) | 30 ms |
| `POST /entitlements/recompute` | 100 ms |
| `POST /addons/drawdown` | 50 ms |

Full metric inventory: `docs/observability/metric-registry.md` (`make check-metrics`)

---

## Key invariants

| ID | Rule |
|---|---|
| ENT-INV-1 | Snapshot version monotonically increasing; upsert only if `incoming > stored` |
| ENT-INV-3 | Drawdown idempotent via `request_id`; duplicate returns existing, no double-deduct |
| ENT-INV-4 | Expired overrides/addons excluded from effective set |
| ENT-INV-5 | Trial overrides only applied when `state == trial` |
| ENT-INV-6 | Recompute is a no-op when computed set equals latest snapshot (no write, no event) |
| CONC-1 | Optimistic locking on 7 tables via `record_version`; `ErrOptimisticLockConflict` on 0 rows |
| EVT-10 | `outbox.Enqueue` inside `RunInTx`; event and snapshot commit atomically |
| RLS-1 | Every tenant-scoped table: RLS enabled+forced, GUC `SET LOCAL` |
| CACHE-1 | Cache errors degrade gracefully; Valkey is never a correctness dependency |

Full invariant list: `.claude/CLAUDE.md` · LLD: `docs/lld/commercial-lld-entitlements-service.md`

---

## Security

| Topic | Guidance |
|---|---|
| **Three-layer isolation** | Kubernetes NetworkPolicy (commercial namespace only); PostgreSQL RLS (`FORCE ROW LEVEL SECURITY` + `tenant_isolation` policy on `app.tenant_id` GUC); audit-tagged writes via platform-audit outbox |
| **RLS-6** | `app.tenant_id` is `SET LOCAL` (transaction-scoped) on every pool checkout — a pooled PgBouncer connection can never leak a GUC value to the next caller; CI greps for any non-`LOCAL` `SET` as a forbidden pattern |
| **Cross-tenant admin** | Requires the `BYPASSRLS` role (`SYSTEM_DATABASE_URL`) — used only by reconciler jobs' cross-tenant sweeps; never the app role |
| **No JWT parsing** | This service trusts gateway/mesh-injected headers and NetworkPolicy; callers are authenticated by the mesh, not by this service |
| **Input boundary** | `limit_key` validated against `domain.KnownLimitKeys`; `tenant_id` validated as UUID; body-supplied values never trusted for authorization |

---

## Deployment (`deploy/helm/`)

**Binaries per Helm release:**
- `Deployment` — `cmd/server` (HTTP + 4 SQS consumers + outbox runner + audit runner)
- `Job` (pre-upgrade hook) — `cmd/migrate` (runs `postgres.MigrateSchema` once per release)
- 5 `CronJob`s — `cmd/reconciler --job=<name>`

**Schema migration:**
- `Deployment` sets `RUN_MIGRATIONS=false` to skip auto-migration in replicas.
- The `migrate-job.yaml` pre-upgrade hook runs `cmd/migrate` exactly once per Helm release.
- `MIGRATION_DATABASE_URL` must be a **direct Postgres DSN** (bypasses PgBouncer — DDL requires a direct connection).

**Health gates:**

| Endpoint | Returns | Checks |
|---|---|---|
| `GET /healthz` | `200 {"status":"ok"}` / `503` | Liveness — audit runner stall only |
| `GET /readyz` | `200` / `503` | Postgres pool, Valkey, outbox runner |

### Reconciler jobs

| Job (`--job=`) | CronJob | Purpose |
|---|---|---|
| `override-expiry` | `commercial-entitlements-override-expiry` | Sweep `entitlement_overrides` where `expires_at <= now` |
| `addon-expiry` | `commercial-entitlements-addon-expiry` | Sweep `quota_addons` where `status='active'` and `expires_at <= now` |
| `outbox-prune` | `commercial-entitlements-outbox-prune` | Delete `outbox_events` rows older than `OUTBOX_RETENTION_DAYS` |
| `processed-events-prune` | `commercial-entitlements-processed-events-prune` | Delete `processed_events` rows older than `PROCESSED_EVENTS_TTL_DAYS` |
| `quota-reset` | `commercial-entitlements-quota-reset` | SCAN+DEL Valkey `quota:usage:*:{key}:{prev_period}` at billing period rollover (ENT-D13) |

---

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | — | App pool DSN (PgBouncer); required |
| `MIGRATION_DATABASE_URL` | — | Direct Postgres DSN for schema migrations (bypasses PgBouncer) |
| `SYSTEM_DATABASE_URL` | — | BYPASSRLS pool for reconciler sweeps; required in non-dev |
| `PG_MAX_CONNS` / `PG_MIN_CONNS` | `20` / `0` | Pool sizing |
| `PG_STATEMENT_TIMEOUT` / `PG_LOCK_TIMEOUT` | `5s` / `2s` | Per-transaction Postgres timeouts |
| `RUN_MIGRATIONS` | `true` | Set `false` in Kubernetes replicas (the Helm migrate Job owns the schema) |
| `VALKEY_URL` | `localhost:6379` | Valkey advisory cache; `rediss://` required outside dev |
| `IAM_CATALOG_BASE_URL` | — | iam-catalog-admin base URL — used by `iamclient` for `FetchPlan` and `FetchAllPlans` (CAT-I2); required |
| `IAM_ORG_BASE_URL` | — | iam-org-membership base URL — used by `iamclient` for `FetchTenantSubscription` (I-17 plan-context); required |
| `IAM_CATALOG_TIMEOUT` | `10s` | Per-request HTTP timeout for IAM calls; override with e.g. `"5s"` or `"15s"` |
| `TEMPORAL_HOST_PORT` | — | Temporal; optional — if unset, catalog fan-out is skipped (degrades gracefully) |
| `SNS_TOPIC_ENTITLEMENTS_ARN` | — | **Required outside dev.** SNS topic for `tenant_entitlements_changed` |
| `SQS_BILLING_ENTITLEMENTS_QUEUE_URL` | — | Billing lifecycle events queue |
| `SQS_TENANT_ENTITLEMENTS_QUEUE_URL` | — | Tenant lifecycle events queue |
| `SQS_CATALOG_ENTITLEMENTS_QUEUE_URL` | — | Plan catalog change events queue |
| `SQS_MEMBERSHIP_ENTITLEMENTS_QUEUE_URL` | — | Membership events queue |
| `SQS_MAX_RECEIVE_COUNT` | `4` | Forward to DLQ at this receive count (one below the RedrivePolicy's 5) |
| `GLUE_REGISTRY_ENTITLEMENTS_NAME` | — | Glue schema registry name; unset → `NoopCodec` (plain JSON) |
| `AWS_REGION` / `AWS_ENDPOINT_URL` | `ap-south-1` / — | LocalStack override in dev; IRSA in production |
| `OUTBOX_POLL_INTERVAL` / `OUTBOX_BATCH_SIZE` / `OUTBOX_MAX_ATTEMPTS` | `500ms` / `50` / `5` | Outbox runner tunables |
| `OUTBOX_RETENTION_DAYS` | `8` | `outbox-prune` job retention |
| `PROCESSED_EVENTS_TTL_DAYS` | `8` | Consumer dedup retention (must be > 7-day SQS maximum retention) |
| `RECONCILER_JOB` / `RECONCILER_TIMEOUT` / `RECONCILER_BATCH_LIMIT` | — / `15m` / `500` | Reconciler dispatcher tunables |
| `PROMETHEUS_PUSHGATEWAY_URL` | — | Where each reconciler run pushes metrics; empty skips the push |
| `APP_NAME` / `APP_ENV` / `APP_PORT` | `commercial-entitlements` / `dev` / `8080` | Service identity and HTTP port |
| `METRICS_PORT` | `9090` | Dedicated `/metrics` listener (separate from `APP_PORT`) |
| `DOCS_ENABLED` / `DOCS_AUTH_TOKEN` | `false` / — | Swagger UI gating outside dev |
| `LOG_LEVEL` | `info` (Helm) / `debug` (dev) | Zap logger level |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` (dev) | OTLP/gRPC trace export |
| `OTEL_TRACES_SAMPLER_RATIO` | `0.1` (Helm) / `1.0` (dev) | Head sampling ratio |

---

## Cross-service dependencies

### 1. Synchronous outbound calls (this service → other services)

Read paths (`GET /entitlements`, quota ceiling reads) have **no** synchronous cross-service dependency — Postgres + Valkey only.

| Operation | Dependency | Adapter package | Posture | On failure |
|---|---|---|---|---|
| Recompute step 1 — fetch tenant plan context (every Recompute) | **IAM Org Membership** `GET /api/v1/internal/tenants/:id/plan-context` (I-17) | `outbound/iamclient/` | fail-closed | Recompute fails; no snapshot written, no event emitted — the consumer will retry |
| Recompute step 2 — fetch plan definition on cache miss | **IAM Org Membership** plan catalog endpoint (via `IAM_CATALOG_BASE_URL`) | `outbound/iamclient/` | fail-closed on cold Postgres cache; falls back to Postgres cache on warm cache | Cold cache + IAM outage → `503`; warm cache → serves from Postgres/Valkey |
| Catalog fan-out — trigger per-tenant Recompute after `PlanCatalogChanged` | **Temporal** (optional) | `temporal/starter.go` | degraded-open | If `TEMPORAL_HOST_PORT` unset or Temporal unavailable, fan-out is skipped; each tenant recomputes on next read |
| Audit log entries (override set/remove, drawdown, shortfall resolved) | **Audit Log Service** (AL-5) | `outbound/auditrecorder/` + platform-audit runner | queued (async) | Entries stay in `pending_audit_entries` and deliver after recovery; the request never fails or waits |
| `cmd/catalog-refresh` — pull plan catalog at startup or on-demand | **IAM Org Membership** plan catalog endpoint | `outbound/iamclient/` | fail-closed | Binary exits non-zero; retried by the calling operator or cron |

**IAM client call is always OUTSIDE `RunInTx`** — no outbound HTTP inside a Postgres transaction (see request-flows: step 1 runs before `TxRunner.RunInTx`).

### 2. Inbound callers (other services → this service)

| Caller | Endpoints used | Purpose |
|---|---|---|
| **Quota Enforcer / Usage & Metering** | ENT-I3 `POST /internal/tenants/:id/entitlements/drawdown` | Record a quota deduction; idempotent via `request_id`; raises shortfall if packs exhausted |
| **In-mesh services** (any service that gate-checks features) | ENT-I1 `GET /internal/tenants/:id/entitlements` | Read the latest effective set without triggering a recompute |
| **In-mesh services / BFF** | ENT-I1b `POST /internal/tenants/:id/entitlements/recompute` | Trigger a full 5-step recompute when the caller knows state has changed |
| **In-mesh services** | ENT-I2 `GET /internal/tenants/:id/plans/current` | Read the tenant's current plan definition from cache |
| **Operator tooling** | ENT-O1..O7 (overrides, trial profile, addons, shortfalls) | Lifecycle management of overrides, addon packs, trial profiles, shortfall resolution |
| **Billing Service** | ENT-O5 `POST /operator/tenants/:id/entitlements/addons` | Grant a quota addon pack after a purchase event |

### 3. Async event dependencies

**Events this service consumes:**

| Queue | Producer | Events handled |
|---|---|---|
| `billing-entitlements-q` | **Billing Service** | `TenantPlanChanged` → Recompute; `TenantSubscriptionCancelled` → Recompute; `TenantReactivated` → Recompute; `TenantQuotaAddOnChanged{reason:purchase}` → ApplyBillingPurchase (grant addon pack); `TenantQuotaAddOnChanged{reason:refund}` → ApplyBillingRefund (remove/adjust pack) |
| `tenant-entitlements-q` | **Realm Provisioner** (on `iam.tenant.events`) | `TenantIdentityProvisioned` → Recompute (initial); `TenantSubscriptionUpdated` → Recompute; `TenantMembershipsPurged` → handleOffboard (Recompute + PurgeTrial); `TenantConverted` → handleOffboard; `TenantOffboarded` → handleOffboard |
| `catalog-entitlements-q` | **Catalog / Admin Config Service** (on `iam.catalog.events`) | `PlanCatalogChanged` → InvalidatePlan (Postgres + Valkey) + Temporal fan-out to all tenants on that plan |
| `membership-entitlements-q` | **IAM Org Membership** (on `iam.membership.events`) | `TenantMembershipsPurged` → Recompute (tenant offboard); `TenantStateChanged{cause:TrialReactivated}` → Recompute (E-3: re-applies trial limits after a plan revert; published after IAM commits so I-17 always reflects the new state) |

**Events this service produces** and their downstream consumers:

| Event | Topic | Consumers |
|---|---|---|
| `tenant_entitlements_changed` | `commercial.entitlements.events` → `SNS_TOPIC_ENTITLEMENTS_ARN` | **Audit Log Service** (catch-all — every change event); **Usage & Metering** (quota ceiling refresh on new snapshot); any downstream service that needs to know when a tenant's limits change |

**Wire format and idempotency:** when `GLUE_REGISTRY_ENTITLEMENTS_NAME` is set, the event payload is wrapped with an 18-byte Glue header (`[0x03][0x00][16-byte schema version UUID]`). The version UUID is resolved once at startup via `GetSchemaByDefinition` — a missing registration is a startup failure, not a runtime error. Consumer dedup: every event processed via `inbox.Store.Process` → `INSERT INTO processed_events ON CONFLICT DO NOTHING` before any side effect.

### 4. Infrastructure dependencies

| System | Role |
|---|---|
| **PostgreSQL** (`commercial_entitlements` DB) | Primary store — 8 tables, `FORCE ROW LEVEL SECURITY`; `app.tenant_id` GUC bound transaction-locally; PgBouncer transaction pooling |
| **Valkey** (Redis-compatible) | Advisory cache (`ent:tenant:*` 5 min; `ent:plans:*` 10 min; `quota:tenant:*` 32 days; `quota:usage:*` 32 days) — a miss or outage falls through to Postgres, never a hard failure (CACHE-1) |
| **AWS SNS** | One topic: `commercial.entitlements.events` (1 event type via `events.Publisher`) |
| **AWS SQS** | 4 inbound queues (billing, tenant, catalog, membership — each with a `-dlq`, `maxReceiveCount=5`) |
| **AWS Glue Schema Registry** | `commercial-entitlements-events` — version UUIDs resolved once at startup; consumers strip the header and validate against embedded schemas |
| **Temporal** (optional) | `CatalogFanoutWorkflow` — fans out `Recompute` to all tenants on a changed plan; skipped when `TEMPORAL_HOST_PORT` is unset |

---

## Out of scope

| Concern | Where it lives |
|---|---|
| Subscription lifecycle, seat counts, feature-flag deltas | IAM Org Membership |
| Plan catalog definitions (global) | Catalog / Admin Config Service |
| Raw usage event ingestion and metering | Usage & Metering |
| Billing plan changes, payment events | Billing Service |
| Tenant provisioning, realm management | Realm Provisioner |
| Display identity, membership roles | IAM Org Membership |
| Workflow execution, tender ACL | Workflow Service / Tender ACL Service |
| Audit delivery of entitlement events | Audit Log Service (subscribes to `tenant_entitlements_changed`) |

---

## Documentation

| File | Contents |
|---|---|
| `.claude/CLAUDE.md` | Commands, binaries, invariants, hooks |
| `.claude/architecture.md` | Package tree, arch-lint boundaries, shared libraries |
| `.claude/api-caching-events.md` | Route catalogue, Valkey key patterns, events |
| `.claude/database-schema.md` | Tables, RLS, migrations, concurrency |
| `.claude/operations.md` | Metrics, reconciler jobs, deployment, config |
| `.claude/request-flows.md` | Step-by-step sequence flows |
| `.claude/key-files.md` | Per-file descriptions for every critical Go file |
| `docs/lld/commercial-lld-entitlements-service.md` | Full LLD (authoritative spec) |
| `docs/observability/metric-registry.md` | Tier 3 metric inventory |
| `CHANGELOG.md` | Change history |
| `CONTRIBUTING.md` | Development guidelines |
