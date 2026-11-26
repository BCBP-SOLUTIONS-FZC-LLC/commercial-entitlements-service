# commercial-entitlements

Go service for the XpertPMS Commercial domain. Resolves each tenant's effective entitlement set from subscription plans, manual overrides, trial assignments, and quota addon packs. Publishes `tenant_entitlements_changed` after every recomputation and enforces quota limits via a drawdown + shortfall workflow.

**Module:** `github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements`

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
| Quota enforcement | Records drawdowns against addon packs; raises shortfalls when packs are exhausted; operator resolves via approve/reject |
| Overrides | Operator-set per-tenant, per-limit-key value overrides with optional expiry |
| Trial profiles | Plan-override bundles applied only while `state == trial`; purged on offboarding |
| Catalog | Read-through over IAM plan definitions (Postgres cache + Valkey; Temporal fan-out on change) |
| Advisory cache | Valkey caches effective sets and plan definitions; quota ceilings and usage counters for sub-millisecond enforcement |

---

## Binaries

| Binary | Source | Purpose |
|---|---|---|
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
      metrics/    ← Prometheus Tier-3 collectors (gincommon)
      iamclient/  ← IAM catalog HTTP client
      auditrecorder/ ← audit log via platform-audit outbox
```

Layer boundary rules are enforced at CI time by `make arch-lint` (`.go-arch-lint.yml`).

Full package tree and architectural decisions: `.claude/architecture.md`

---

## Infrastructure dependencies

| Service | Port | Purpose |
|---|---|---|
| PostgreSQL 16 | 5432 | State, RLS, transactional outbox (`outbox_events`), audit (`pending_audit_entries`) |
| Valkey 7 | 6379 | Advisory cache — effective sets, plan definitions, quota ceilings, usage counters |
| Temporal | 7233 | Optional — catalog fan-out workflow; skipped when `TEMPORAL_HOST_PORT` is unset |
| LocalStack / AWS | 4566 | SNS (produced events), SQS (consumed events), Glue schema registry |

---

## API surface

Three route groups, all under `/api/v1/`:

| Group | Prefix | Consumers |
|---|---|---|
| Internal | `/api/v1/internal/tenants/:id/` | In-mesh services (quota enforcer, IAM) |
| Tenant | `/api/v1/tenants/:id/` | Tenant-scoped JWT callers |
| Operator | `/api/v1/operator/` | Operator tooling |

Full route catalogue: `.claude/api-caching-events.md`
Swagger UI: `GET /swagger/index.html` (requires `DOCS_ENABLED=true`)

---

## Events

**Produced:** `tenant_entitlements_changed` → `SNS_TOPIC_ENTITLEMENTS_ARN`

**Consumed:**

| Queue | Consumer | Key events |
|---|---|---|
| `billing-entitlements-q` | `BillingEventConsumer` | `TenantPlanChanged`, `TenantSubscriptionCancelled`, `TenantReactivated`, `TenantQuotaAddOnChanged` |
| `tenant-entitlements-q` | `TenantEventConsumer` | `TenantIdentityProvisioned`, `TenantSubscriptionUpdated`, `TenantMembershipsPurged`, `TenantConverted`, `TenantOffboarded` |
| `catalog-entitlements-q` | `CatalogConsumer` | `PlanCatalogChanged` |
| `membership-entitlements-q` | `MembershipConsumer` | `TenantMembershipsPurged`, `TenantStateChanged{cause:TrialReactivated}` |

Event contracts: `api/asyncapi.yaml` · JSON schemas: `internal/adapter/outbound/eventbus/schemas/`

---

## Observability

- **Metrics:** `commercial_entitlements_*` (Tier 3, `gincommon.NewCounterVec` etc.) + `platform_*` (Tier 1, via `events.InitMetrics`, `pgmetrics.InitWithIdentity`, `audit.InitMetrics`). Domain: `commercial`. Scrape: `METRICS_PORT` (default 9090).
- **Tracing:** OTel via `gincommon.InitTracingWithConfig`. W3C `traceparent` propagated to IAM client and SQS consumer spans.
- **Logging:** Structured JSON via `port.Logger` → `platform-gincommon/pkg/logger`.
- **Alerts:** `deploy/monitoring/app-alerts.yml`, `deploy/helm/templates/prometheusrule.yaml`.

---

## Key invariants

| ID | Rule |
|---|---|
| ENT-INV-1 | Snapshot version monotonically increasing; upsert only if `incoming > stored` |
| ENT-INV-3 | Drawdown idempotent via `request_id` |
| ENT-INV-6 | Recompute is a no-op when computed set equals latest snapshot |
| CONC-1 | Optimistic locking on 7 tables via `record_version` |
| EVT-10 | `outbox.Enqueue` inside `RunInTx`; event and state commit atomically |
| RLS-1 | Every tenant-scoped table: RLS enabled+forced, GUC `SET LOCAL` |
| CACHE-1 | Cache errors degrade gracefully; Valkey is never a correctness dependency |

Full invariant list: `.claude/CLAUDE.md` · LLD: `docs/lld/commercial-lld-entitlements-service.md`

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
