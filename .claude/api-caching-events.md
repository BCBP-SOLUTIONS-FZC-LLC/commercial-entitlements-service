# API, Caching & Events

## API Conventions

- All routes under `/internal/v1/`. Service is **internal-only** — no public internet exposure; called only by in-mesh services over mTLS.
- Middleware: `gincommon.ObservabilityMiddlewares` (`PanicRecovery → RequestID → Tracing → CorrelationHeaders → Metrics → Logging → Timeout`, 30s) → `gincommon.ProtectedMiddlewares` (`RequireAuth → ContextMiddleware`) → route-specific gates.
- Content type `application/json`. Timestamps RFC 3339 UTC. Errors via `gincommon.ErrorResponse`.
- All routes require `x-tenant-id` header (set by the calling service); RLS enforces the boundary.
- Swagger UI: `GET /docs/swagger/index.html` (requires `DOCS_ENABLED=true` and `DOCS_AUTH_TOKEN` in staging/prod).

## Endpoint Catalogue

Base prefix: `/internal/v1/tenants/:tenant_id`

### Entitlements

| Method & Path | Purpose | Handler |
|---|---|---|
| `GET /entitlements` | Get latest effective set (from cache/snapshot, no recompute) | `EntitlementHandler.GetEffectiveSet` |
| `POST /entitlements/recompute` | Trigger full 5-step recompute; body `{cause}` | `EntitlementHandler.Recompute` |

### Overrides (operator manual limit overrides)

| Method & Path | Purpose | Handler |
|---|---|---|
| `PUT /overrides/:limit_key` | Set or update an override for a limit key | `OverrideHandler.SetOverride` |
| `DELETE /overrides/:limit_key` | Remove an override | `OverrideHandler.RemoveOverride` |
| `GET /overrides/:limit_key` | Get a single active override | `OverrideHandler.GetOverride` |
| `GET /overrides` | List all active overrides for a tenant | `OverrideHandler.ListOverrides` |
| `GET /overrides/:limit_key/history` | Full override lifecycle history | `OverrideHandler.ListOverrideHistory` |

### Addons (quota addon packs)

| Method & Path | Purpose | Handler |
|---|---|---|
| `POST /addons` | Grant a new quota addon pack | `AddonHandler.AddAddon` |
| `DELETE /addons/:addon_id` | Revoke an addon | `AddonHandler.RemoveAddon` |
| `GET /addons` | List all addon packs (optionally filtered by limit_key) | `AddonHandler.ListAddons` |
| `POST /addons/drawdown` | Record a quota drawdown (idempotent via request_id) | `AddonHandler.RecordDrawdown` |

### Shortfalls

| Method & Path | Purpose | Handler |
|---|---|---|
| `GET /shortfalls` | List shortfalls (optionally filtered by status) | `ShortfallHandler.ListShortfalls` |
| `POST /shortfalls/:shortfall_id/resolve` | Approve or reject a pending shortfall | `ShortfallHandler.ResolveShortfall` |

### Trials

| Method & Path | Purpose | Handler |
|---|---|---|
| `POST /trial` | Set trial assignment (profile + dates) | `TrialHandler.SetTrial` |
| `DELETE /trial` | Purge all trial data for a tenant | `TrialHandler.PurgeTrial` |
| `GET /trial` | Get current trial assignment | `TrialHandler.GetTrialAssignment` |
| `PUT /trial/overrides/:limit_key` | Set a trial-scoped limit override | `TrialHandler.SetTrialOverride` |
| `DELETE /trial/overrides/:limit_key` | Remove a trial-scoped override | `TrialHandler.RemoveTrialOverride` |
| `GET /trial/overrides` | List trial overrides for a tenant | `TrialHandler.ListTrialOverrides` |

### Plans (catalog read)

Base prefix: `/internal/v1/plans`

| Method & Path | Purpose | Handler |
|---|---|---|
| `GET /plans/:plan_code` | Get a plan definition (from Postgres cache; IAM on miss) | `PlansHandler.GetPlan` |
| `GET /plans` | List all cached plan codes | `PlansHandler.ListPlans` |
| `POST /plans/refresh` | Trigger manual catalog refresh for a plan | `PlansHandler.RefreshCatalog` |

### Health (standard platform)

| Method & Path | Purpose |
|---|---|
| `GET /healthz` | Liveness (audit runner liveness) |
| `GET /readyz` | Readiness (Postgres, Valkey, Outbox ready) |
| `GET /metrics` | Prometheus metrics (on `METRICS_PORT`, separate server) |

## Caching (Valkey, `internal/adapter/outbound/valkey/cache.go`)

Cache is **advisory only** (CACHE-1). Every cache error falls through to Postgres — never fails the business operation.

### Key patterns and TTLs

| Key pattern | TTL | Purpose |
|------------|-----|---------|
| `ent:snapshot:{tenant_id}` | 5 min | Latest effective set (GetEffectiveSet fast path) |
| `quota:ceiling:{tenant_id}:{limit_key}` | 32 days | Quota enforcer ceiling; written by Recompute (ENT-D13) |
| `quota:usage:{tenant_id}:{limit_key}:{YYYY-MM}` | 32 days | Per-period usage counter; incremented by quota enforcer; reset by quota-reset reconciler |
| `ent:plan:{plan_code}` | 10 min | Plan definition cache (CatalogService read-through) |

### Cache invariants (CACHE-1..2)

- **CACHE-1** — Cache is advisory. A miss, timeout, or Valkey outage always falls through to Postgres. Never fail a business operation on a cache error.
- **CACHE-2** — `SetEffectiveSet` is called after every successful snapshot write (Recompute). The key is the same as `GetEffectiveSet`'s key — cache stays warm after every write.

### ENT-D13: Quota ceiling key

After every `Recompute`, `writeQuotaCeiling` writes `quota:ceiling:{tenant_id}:{limit_key}` for `llm_token_quota_monthly` and `api_request_budget_monthly`. The quota enforcer reads this to gate requests without hitting Postgres on every call. If the key is absent, the enforcer falls back to 0 (unlimited, since the ceiling hasn't been established yet).

### DeleteUsageCounters

The `quota-reset` reconciler calls `Cache.DeleteUsageCounters(ctx, limitKey, prevPeriod)` which uses Redis `SCAN MATCH quota:usage:*:{limitKey}:{period} DEL` to atomically clear all tenants' stale usage counters at billing period rollover — no tenant enumeration from Postgres needed.

## Events

### Produced

| Event type | Schema | Trigger | Topic |
|-----------|--------|---------|-------|
| `tenant_entitlements_changed` | `internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json` | Every successful `Recompute` that writes a new snapshot version | `SNS_TOPIC_ENTITLEMENTS_ARN` |

Payload: `{tenant_id, version, previous_version, cause, computed_at}`.

Schema governance: `api/asyncapi.yaml` is the design-time source of truth. Schemas in `eventbus/schemas/*.json` are embedded via `//go:embed` in the `ValidatingCodec` and must be pre-registered in Glue Schema Registry (resolved once at startup via `GetSchemaByDefinition`).

### Consumed

| Queue env var | Queue name | Consumer | Event types handled |
|---|---|---|---|
| `SQS_BILLING_ENTITLEMENTS_QUEUE_URL` | `billing-entitlements-q` | `BillingEventConsumer` | `TenantPlanChanged`, `TenantSeatsChanged`, `TenantSubscriptionCancelled`, `TenantReactivated` → Recompute |
| `SQS_TENANT_ENTITLEMENTS_QUEUE_URL` | `tenant-entitlements-q` | `TenantEventConsumer` | `TenantProvisioned`, `TenantConverted`, `TenantSuspended`, `TenantOffboarded` → Recompute; `TrialExpired` → Recompute + PurgeTrial |
| `SQS_CATALOG_ENTITLEMENTS_QUEUE_URL` | `catalog-entitlements-q` | `CatalogConsumer` | `PlanCatalogChanged` → InvalidatePlan cache + Temporal fan-out |
| `SQS_MEMBERSHIP_ENTITLEMENTS_QUEUE_URL` | `membership-entitlements-q` | `MembershipConsumer` | `TenantMembershipsPurged` → Recompute; `TenantStateChanged{cause:TrialReactivated}` → Recompute (E-3 read-race fix; published after O&M commit via EVT-16) |

Each queue has a `-dlq` with `maxReceiveCount = 5` RedrivePolicy. `platform-events` forwards at `SQS_MAX_RECEIVE_COUNT` 4 (one below the RedrivePolicy). Idempotency via `processed_events` / `inbox.Store.Process`.

**All consumers:**
- Dedup: `processOnce` / `ackUnknown` from `consumer/dedup.go`; `InboxRepository.ProcessOnce` claims and applies in one tx.
- DLQ: via `events.WithDLQForwarding` in `sqspipeline.NewConsumer`.
- Unknown event types: forwarded to DLQ if consume schema validation fails; otherwise acked via `ackUnknown`.

## Event schema registration

Every new event type produced by this service needs:
1. A JSON Schema Draft-07 file in `internal/adapter/outbound/eventbus/schemas/`.
2. An entry in `api/asyncapi.yaml`.
3. Pre-registration in AWS Glue Schema Registry (the `GlueCodec` resolves the version UUID by definition at startup — a missing registration is a startup failure, not a runtime error).
