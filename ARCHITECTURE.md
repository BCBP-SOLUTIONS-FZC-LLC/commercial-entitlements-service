# Architecture

This document describes the internal structure, dependency rules, and runtime data flows of `commercial-entitlements`.

`commercial-entitlements` is a **private Go service** (`github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements`, Go 1.26.11) deployed as a containerised microservice (HPA 2–8 replicas). It owns the **entitlement layer** of the XpertPMS commercial domain — per-tenant entitlement snapshots, quota addon packs, manual overrides, trial assignments, drawdown requests, and shortfall workflows. It is the source of truth for plan-based limits, quota ceilings, and usage counters consumed by Billing and other commercial services. It ships as **six binaries from one image**: `cmd/server` (the full HTTP surface — internal/tenant/operator — plus four inbound SQS consumers, the transactional outbox runner, the platform-audit outbox runner, and an optional Temporal workflow starter), `cmd/reconciler` (a single binary dispatched by `--job=<name>`, covering 5 CronJobs), `cmd/worker` (Temporal workflow worker for catalog fan-out), `cmd/migrate` (applies the schema once per release as a Helm pre-install/pre-upgrade hook Job), `cmd/expiry` (standalone addon/override expiry sweep), and `cmd/catalog-refresh` (pulls the IAM plan catalog into Postgres + Valkey).

**Owns:** entitlement snapshots (`entitlement_snapshots`), overrides (`entitlement_overrides`, `entitlement_override_history`), quota addons (`quota_addons`, `quota_addon_drawdowns`, `quota_addon_shortfalls`), trial assignments (`tenant_trial_assignments`, `trial_entitlement_overrides`), plan catalog cache (`plan_catalog_cache`), inbox dedup (`processed_events`), transactional outbox (`outbox_events`), audit outbox (`pending_audit_entries`).

**Does not own:** billing subscription lifecycle, payment processing, Keycloak/IAM user management, department/membership data (IAM Org Membership), raw plan definitions (IAM Catalog Admin — this service reads them via `IAM_CATALOG_BASE_URL`), audit records (Audit Log).

---

## Layer model

The service is organised in concentric Clean Architecture layers. Inner layers have **zero knowledge** of outer layers; dependencies always point inward.

> Source: [`docs/architecture/mermaid/layer-model.mmd`](docs/architecture/mermaid/layer-model.mmd)

```mermaid
graph TD
    subgraph cmd["Composition Roots — cmd/ (six binaries, one image)"]
        server["cmd/server\nwire all deps: pgcommon.NewPool + GUCProvider (RLS-6) + BYPASSRLS sysPool\nRUN_MIGRATIONS false (Helm hook owns it)\n4 SQS consumers (billing/tenant/catalog/membership-entitlements-q)\noutbox runner + auditoutbox runner\nTemporal client starter (optional, skipped when TEMPORAL_HOST_PORT unset)\ngraceful shutdown: SIGTERM → HTTP + SQS drain (≤30s) → outbox (30s) → pools\nterminationGracePeriodSeconds=75"]
        reconciler["cmd/reconciler\n--job=<name> dispatch, 5 CronJobs:\n  override-expiry · addon-expiry\n  outbox-prune · processed-events-prune · quota-reset\nRECONCILER_STATEMENT_TIMEOUT (2m)"]
        worker["cmd/worker\nTemporal workflow worker\ncatalog fan-out workflow"]
        migrate_bin["cmd/migrate\nMIGRATION_DATABASE_URL direct, MIGRATE_TIMEOUT=10m\nHelm pre-install/pre-upgrade hook Job"]
        expiry_bin["cmd/expiry\nstandalone addon/override expiry sweep"]
        catrefresh["cmd/catalog-refresh\npulls IAM plan catalog into Postgres + Valkey\ncalls IAM_CATALOG_BASE_URL"]
    end

    subgraph adapters_in["Inbound Adapters — internal/adapter/inbound/"]
        http_h["http/\nrouter.go · middleware.go · handlers\nENT-I1/I2/I3 (internal), ENT-T1 (tenant), ENT-O1..O7 (operator)\nBindPathTenant · HandleError · NormalizeAuthErrors\nserves /asyncapi.yaml from api/embed.go"]
        sqspipeline["sqspipeline/\nplatform-events GlueDecodeCodec + DLQ router\nconsumed-schema validation · inbox.ProcessOnce claim\nRequireDLQ fatal at startup"]
        consumer["consumer/\nbilling_consumer.go  ← billing-entitlements-q\ntenant_consumer.go   ← tenant-entitlements-q\ncatalog_consumer.go  ← catalog-entitlements-q\nmembership_consumer.go ← membership-entitlements-q\ndedup.go processOnce/ackUnknown over port.Inbox"]
    end

    subgraph adapters_out["Outbound Adapters — internal/adapter/outbound/"]
        postgres["postgres/\nEntitlementRepository · OverrideRepository\nAddonRepository · ShortfallRepository · TrialRepository\nIdempotencyRepository (inbox.NewStore)\nplan_catalog_cache table access\nOptimistic locking (record_version) on 7 tables (CONC-1)\nmigrations/ 000001_initial_schema + 000002_addon_billing_ref"]
        valkey["valkey/\nValkeyCache\neff-set: ent:tenant:{id}  TTL 300s\nplan-def: ent:plans:{code}  TTL 600s\nquota-ceil: quota:tenant:{id}:{key}  TTL 330s\nusage-ctr: quota:usage:{id}:{key}:{period}  TTL=period end"]
        eventbus["eventbus/\nPublisher.EnqueueOrdered keyed {tenant_id} under advisory lock\nGlueCodec (commercial-entitlements-events)\nschemas/tenant_entitlements_changed.json embedded via go:embed"]
        iamclient["iamclient/\nIAMCatalogClient (HTTP)\nGET /internal/plans from IAM_CATALOG_BASE_URL"]
        auditrecorder["auditrecorder/\nplatform-audit auditoutbox.Enqueuer\npending_audit_entries in business tx (PA-INV-3)"]
        metrics["metrics/\nregistry.go — 19 commercial_entitlements_* Tier 3 collectors\nbusiness.go — nil-safe helpers + prewarm"]
    end

    subgraph temporal_grp["Temporal Adapter — internal/adapter/temporal/"]
        temporal["catalog_fanout_workflow.go\nactivities.go · starter.go\nexecutes catalog refresh via Temporal"]
    end

    subgraph core["Core — internal/core/"]
        service["service/\nEntitlementsService · DrawdownService\nOverrideService · TrialService · ShortfallService"]
        port["port/\ninbound.go — service interfaces\noutbound.go — repo/cache/publisher/inbox interfaces\ncontext.go — tx/event-publisher context keys"]
        domain["domain/\nEntitlementSnapshot · Override · QuotaAddon\nQuotaAddonDrawdown · QuotaAddonShortfall\nTenantTrialAssignment · Plan · EffectiveSet\nDomainError catalogue · InboundEvent types"]
    end

    subgraph schemas_grp["Embedded Schemas — eventbus/schemas/"]
        eventschema["tenant_entitlements_changed.json\nJSON Schema Draft-07, CLOSED (additionalProperties:false)\nDerived via schema-gov extract from api/asyncapi.yaml"]
    end

    subgraph apispec_grp["Embedded API spec — api/"]
        apispec["asyncapi.yaml · embed.go\nSingle source of truth for the event contract\nServed at /asyncapi.yaml"]
    end
```

**Rule:** `domain` ← `port` ← `service` ← `adapter` ← `cmd`. Nothing in `core/` imports `adapter/`. `service` may import `internal/adapter/outbound/metrics` directly (business-outcome counters at the point of the outcome). The inbound side has two components: `adapters_inbound` (`http`, `consumer`) and `inbound_pipeline` (`sqspipeline`, which wraps every consumer's `Handle`). Enforced in CI by `go-arch-lint` (`.go-arch-lint.yml`, `deepScan: false`).

---

## Package dependency graph

> Source: [`docs/architecture/mermaid/package-dependencies.mmd`](docs/architecture/mermaid/package-dependencies.mmd)

```mermaid
graph LR
    server_cmd(["cmd/server"])
    reconciler_cmd(["cmd/reconciler"])
    worker_cmd(["cmd/worker"])
    migrate_cmd(["cmd/migrate"])
    expiry_cmd(["cmd/expiry"])
    catrefresh_cmd(["cmd/catalog-refresh"])

    http_in(["adapter/inbound/http"])
    consumer_in(["adapter/inbound/consumer"])
    pipeline_in(["adapter/inbound/sqspipeline"])

    postgres_out(["adapter/outbound/postgres"])
    valkey_out(["adapter/outbound/valkey"])
    eventbus_out(["adapter/outbound/eventbus"])
    iamclient_out(["adapter/outbound/iamclient"])
    auditrecorder_out(["adapter/outbound/auditrecorder"])
    metrics_out(["adapter/outbound/metrics"])
    temporal_adp(["adapter/temporal"])

    service_core(["core/service"])
    port_core(["core/port"])
    domain_core(["core/domain"])
    eventschema_a(["eventbus/schemas"])
    apispec_a(["api"])
    docs_a(["docs/swagger"])

    server_cmd --> http_in & consumer_in & pipeline_in
    server_cmd --> postgres_out & valkey_out & eventbus_out & iamclient_out & auditrecorder_out & metrics_out
    server_cmd --> temporal_adp & service_core & docs_a
    reconciler_cmd --> postgres_out & valkey_out & metrics_out & service_core
    worker_cmd --> temporal_adp & service_core
    catrefresh_cmd --> iamclient_out & postgres_out & valkey_out
    expiry_cmd --> postgres_out & metrics_out

    http_in --> service_core & port_core & domain_core & metrics_out & apispec_a
    consumer_in --> service_core & port_core & domain_core & metrics_out
    pipeline_in --> consumer_in & port_core & domain_core & metrics_out

    postgres_out --> port_core & domain_core
    valkey_out --> port_core & domain_core
    eventbus_out --> port_core & domain_core & eventschema_a
    iamclient_out --> domain_core & port_core
    auditrecorder_out --> port_core
    metrics_out --> domain_core
    temporal_adp --> domain_core & port_core & service_core

    service_core --> port_core & domain_core & metrics_out
    port_core --> domain_core
```

---

## HTTP request flow — recompute hot path

> Source: [`docs/architecture/mermaid/request-flow.mmd`](docs/architecture/mermaid/request-flow.mmd)

`POST /api/v1/internal/tenants/:id/entitlements/recompute` (ENT-I1b):
1. `BindPathTenant` middleware injects `tenantID` into request context; sets `app.tenant_id` GUC via `pgcommon.GUCSetFromContext` for the tx.
2. `EntitlementHandler.Recompute` → `EntitlementsService.RecomputeEntitlements(ctx, tenantID, cause)`.
3. Inside `RunInTx`: read `entitlement_snapshots` (with `record_version` for CONC-1), load plan from `plan_catalog_cache` (fall back to `IAMCatalogClient` on miss), apply active overrides + addons + trial profile.
4. If computed set differs from stored (ENT-INV-6): `UpsertSnapshot` (WHERE `record_version = current`, ENT-INV-1) + `Publisher.EnqueueOrdered(tx, TenantEntitlementsChanged, orderingKey=tenantID)` (EVT-10, advisory lock).
5. COMMIT — snapshot + outbox row atomic. Invalidate `ent:tenant:{id}` in Valkey.
6. Outbox runner polls every 500ms → `GlueCodec.Encode` → `sns:Publish(commercial-entitlements-events)`.

---

## Write flow — transactional outbox

> Source: [`docs/architecture/mermaid/write-flow.mmd`](docs/architecture/mermaid/write-flow.mmd)

---

## Data model

> Source: [`docs/architecture/mermaid/data-model.mmd`](docs/architecture/mermaid/data-model.mmd)

Ten tables in `commercial_entitlements`:

| Table | Optimistic lock | Purpose |
|---|---|---|
| `entitlement_snapshots` | ✓ | Per-tenant effective set + plan state (ENT-INV-1) |
| `entitlement_overrides` | ✓ | Manual limit overrides (ENT-INV-4/5) |
| `entitlement_override_history` | — | Immutable override audit trail |
| `quota_addons` | ✓ | Quota addon packs |
| `quota_addon_drawdowns` | — | Idempotent usage deductions (ENT-INV-3) |
| `quota_addon_shortfalls` | ✓ | Shortfall resolution state |
| `tenant_trial_assignments` | ✓ | Trial profile assignment |
| `trial_entitlement_overrides` | — | Per-limit trial overrides |
| `plan_catalog_cache` | — | Soft-synced plan definitions from IAM Catalog |
| `processed_events` | — | Inbox dedup (platform-events) |

Plus platform library tables: `outbox_events`, `outbox_dead_letters`, `pending_audit_entries`.

Optimistic locking (`record_version` in `WHERE`, `ErrOptimisticLockConflict` on 0 rows) is on 7 of the 10 tables (CONC-1).

---

## Valkey cache strategy

> Source: [`docs/architecture/mermaid/cache-strategy.mmd`](docs/architecture/mermaid/cache-strategy.mmd)

| Key pattern | TTL | Written by | Invalidated by |
|---|---|---|---|
| `ent:tenant:{tenantID}` | 300s | `RecomputeEntitlements` | Override/addon/trial mutation |
| `ent:plans:{planCode}` | 600s | `catalog-refresh` | Billing plan-change event |
| `quota:tenant:{tenantID}:{limitKey}` | 330s | `RecomputeEntitlements` | Addon grant/expire/refund |
| `quota:usage:{tenantID}:{limitKey}:{period}` | period end | Drawdown commit | `quota-reset` reconciler |

**CACHE-1:** Valkey is advisory only. Cache errors degrade gracefully — service falls back to Postgres. No RLS on Valkey; tenant isolation is enforced at the repository layer before any cache write.

---

## Event / consumer topology

> Source: [`docs/architecture/mermaid/event-outbox-flow.mmd`](docs/architecture/mermaid/event-outbox-flow.mmd)

**Produced:**

| Event | Topic | Registry |
|---|---|---|
| `TenantEntitlementsChanged` | `commercial-entitlements-events` | `commercial-entitlements-events` |

**Consumed:**

| Queue | Source topic | EventType filter |
|---|---|---|
| `billing-entitlements-q` | `billing-events` | `TenantPlanChanged`, `TenantSubscriptionCancelled`, `TenantReactivated`, `TenantQuotaAddOnChanged` |
| `tenant-entitlements-q` | `iam-tenant-events` | `TenantIdentityProvisioned`, `TenantSubscriptionUpdated`, `TenantMembershipsPurged`, `TenantConverted`, `TenantOffboarded` |
| `catalog-entitlements-q` | `iam-catalog-events` | `PlanCatalogChanged` |
| `membership-entitlements-q` | `iam-membership-events` | `TenantMembershipsPurged`, `TenantStateChanged` |

---

## RLS / GUC flow

> Source: [`docs/architecture/mermaid/rls-guc-flow.mmd`](docs/architecture/mermaid/rls-guc-flow.mmd)

Every tenant-scoped table has `ROW SECURITY ENABLED + FORCE ROW SECURITY`. The `tenant_isolation` RLS policy checks `current_setting('app.tenant_id')` against `tenant_id`. The GUC is set as `SET LOCAL app.tenant_id = …` (transaction-local only) via `pgcommon.GUCSetFromContext` → never session-scoped, never leaks across PgBouncer pooled backends (**RLS-6**).

---

## Observability

> Source: [`docs/architecture/mermaid/observability-stack.mmd`](docs/architecture/mermaid/observability-stack.mmd)

See [`docs/observability/README.md`](docs/observability/README.md) for the full metric inventory, tier breakdown, and CI enforcement rules.

| Label | Value |
|---|---|
| `domain` | `commercial` |
| `service` | `commercial-entitlements` |
| `environment` | `APP_ENV` (`local\|dev\|test\|staging\|prod`) |

Three-tier metrics: Tier 1 `platform_*` (platform libraries), no Tier 2 yet, Tier 3 `commercial_entitlements_*` (19 collectors in `internal/adapter/outbound/metrics/registry.go`).

Reconciler CronJobs share the server identity and push to `PROMETHEUS_PUSHGATEWAY_URL`, grouped by `cronjob`.
