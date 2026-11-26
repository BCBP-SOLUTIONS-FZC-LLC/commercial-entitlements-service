# API, Caching & Events

## API Conventions

- **Route prefix:** `/api/v1/` with three route groups: `/api/v1/internal/`, `/api/v1/tenants/`, `/api/v1/operator/`. Service is **internal-only** — no public internet exposure; called only by in-mesh services over mTLS.
- **Source of truth:** `internal/adapter/inbound/http/router.go`.
- Middleware: `gincommon.ObservabilityMiddlewares` (`PanicRecovery → RequestID → Tracing → CorrelationHeaders → Metrics → Logging → Timeout`, 30s) → `gincommon.ProtectedMiddlewares` (`RequireAuth → ContextMiddleware`) → route-specific gates.
- Content type `application/json`. Timestamps RFC 3339 UTC. Errors via `gincommon.ErrorResponse`.
- All routes require `x-tenant-id` header (set by the calling service); RLS enforces the boundary.
- Swagger UI: `GET /swagger/index.html` (requires `DOCS_ENABLED=true`; `DOCS_AUTH_TOKEN` required in staging/prod). Generate with `make swag`.

## Endpoint Catalogue

### Internal routes — `/api/v1/internal/tenants/:id/`

Called by in-mesh services that need raw entitlement data or quota enforcement. No JWT required; NetworkPolicy enforced.

| Method & Full Path | Ref | Purpose | Handler |
|---|---|---|---|
| `GET /api/v1/internal/tenants/:id/entitlements` | ENT-I1 | Get latest effective set (cache → snapshot; no recompute) | `EntitlementHandler.GetEffectiveSetInternal` |
| `POST /api/v1/internal/tenants/:id/entitlements/recompute` | ENT-I1b | Trigger full 5-step recompute; body `{cause}` | `EntitlementHandler.Recompute` |
| `GET /api/v1/internal/tenants/:id/plans/current` | ENT-I2 | Get the tenant's current plan definition | `PlansHandler.GetCurrentPlan` |
| `POST /api/v1/internal/tenants/:id/entitlements/drawdown` | ENT-I3 | Record a quota drawdown (idempotent via `request_id`) | `AddonHandler.RecordDrawdownInternal` |

### Tenant routes — `/api/v1/tenants/:id/`

JWT-scoped — tenant sees only its own entitlements.

| Method & Full Path | Ref | Purpose | Handler |
|---|---|---|---|
| `GET /api/v1/tenants/:id/entitlements` | ENT-T1 | Get latest effective set (tenant-scoped JWT) | `EntitlementHandler.GetEffectiveSetTenant` |

### Operator routes — `/api/v1/operator/tenants/:id/`

Operator tooling (in-mesh mTLS). All mutations are recorded in `override_history`.

**Overrides (`/api/v1/operator/tenants/:id/entitlements/overrides`)**

| Method & Path | Ref | Purpose | Handler |
|---|---|---|---|
| `GET /entitlements/overrides` | ENT-O1 | List all active overrides for a tenant | `OverrideHandler.List` |
| `GET /entitlements/overrides/:limitKey` | ENT-O1 | Get a single active override | `OverrideHandler.Get` |
| `PUT /entitlements/overrides/:limitKey` | ENT-O2 | Set or update an override | `OverrideHandler.Set` |
| `DELETE /entitlements/overrides/:limitKey` | ENT-O2 | Remove an override | `OverrideHandler.Remove` |
| `GET /entitlements/overrides/:limitKey/history` | ENT-O1 | Full override lifecycle history | `OverrideHandler.History` |

**Trial profile (`/api/v1/operator/tenants/:id/entitlements/trial-profile`)**

| Method & Path | Ref | Purpose | Handler |
|---|---|---|---|
| `PUT /entitlements/trial-profile` | ENT-O3 | Assign or update trial profile | `TrialHandler.AssignProfile` |
| `GET /entitlements/trial-profile` | ENT-O3 | Get current trial assignment | `TrialHandler.GetAssignment` |
| `DELETE /entitlements/trial-profile` | ENT-O3 | Purge all trial data for a tenant | `TrialHandler.PurgeTrial` |

**Trial overrides (`/api/v1/operator/tenants/:id/entitlements/trial-overrides`)**

| Method & Path | Ref | Purpose | Handler |
|---|---|---|---|
| `GET /entitlements/trial-overrides` | ENT-O4 | List trial-scoped limit overrides | `TrialHandler.ListOverrides` |
| `PUT /entitlements/trial-overrides/:limitKey` | ENT-O4 | Set a trial-scoped override | `TrialHandler.SetOverride` |
| `DELETE /entitlements/trial-overrides/:limitKey` | ENT-O4 | Remove a trial-scoped override | `TrialHandler.RemoveOverride` |

**Addons & shortfalls (`/api/v1/operator/tenants/:id/entitlements/addons`)**

| Method & Path | Ref | Purpose | Handler |
|---|---|---|---|
| `GET /entitlements/addons` | ENT-O5 | List all addon packs (optionally by `limit_key`) | `AddonHandler.List` |
| `POST /entitlements/addons` | ENT-O5 | Grant a new quota addon pack | `AddonHandler.Add` |
| `DELETE /entitlements/addons/:addonId` | ENT-O5 | Revoke an addon pack | `AddonHandler.Remove` |
| `GET /entitlements/addons/drawdowns` | ENT-O5 | List drawdown records | `AddonHandler.ListDrawdowns` |
| `GET /entitlements/addons/shortfalls` | ENT-O6 | List shortfalls (optionally by `status`) | `ShortfallHandler.List` |
| `POST /entitlements/addons/shortfalls/:shortfallId/resolve` | ENT-O7 | Approve or reject a pending shortfall | `ShortfallHandler.Resolve` |

**Plans (`/api/v1/operator/plans`)**

| Method & Full Path | Purpose | Handler |
|---|---|---|
| `GET /api/v1/operator/plans` | List all cached plan codes | `PlansHandler.ListPlans` |
| `POST /api/v1/operator/plans/:planCode/refresh` | Trigger manual catalog refresh for a plan | `PlansHandler.RefreshCatalog` |

### Health & observability (root)

| Method & Path | Purpose |
|---|---|
| `GET /healthz` | Liveness (audit runner liveness) |
| `GET /readyz` | Readiness (Postgres, Valkey, Outbox ready) |
| `GET /asyncapi.yaml` | Serve embedded `api/asyncapi.yaml` |
| `GET /asyncapi` | AsyncAPI Studio UI (when `DOCS_ENABLED=true`) |
| `GET /metrics` | Prometheus metrics (on `METRICS_PORT`, separate mux) |

## Caching (Valkey, `internal/adapter/outbound/valkey/cache.go`)

Cache is **advisory only** (CACHE-1). Every cache error falls through to Postgres — never fails the business operation.

### Key patterns and TTLs

All keys are derived by the functions in `valkey/cache.go`. Use these exact strings for SCAN patterns in runbooks.

| Key pattern | TTL | Function | Purpose |
|------------|-----|---------|---------|
| `ent:tenant:{tenant_id}` | 5 min | `effectiveSetKey` | Latest effective set (GetEffectiveSet fast path) |
| `ent:plans:{plan_code}` | 10 min | `planKey` | Plan definition cache (CatalogService read-through) |
| `quota:tenant:{tenant_id}:{limit_key}` | 32 days | `quotaCeilingKey` | Quota enforcer ceiling; written by Recompute (ENT-D13) |
| `quota:usage:{tenant_id}:{limit_key}:{YYYY-MM}` | 32 days | `usageCounterKey` | Per-period usage counter; incremented by quota enforcer; reset by quota-reset reconciler |

**TTL rationale:** `quotaCeilingTTL = 32 * 24 * time.Hour` — slightly longer than a billing month to tolerate clock skew on the first request of a new period (ENT-D13).

### Cache invariants (CACHE-1..2)

- **CACHE-1** — Cache is advisory. A miss, timeout, or Valkey outage always falls through to Postgres. Never fail a business operation on a cache error.
- **CACHE-2** — `SetEffectiveSet` is called after every successful snapshot write (Recompute). The key is the same as `GetEffectiveSet`'s key — cache stays warm after every write.

### ENT-D13: Quota ceiling key

After every `Recompute`, `writeQuotaCeiling` writes `quota:tenant:{tenant_id}:{limit_key}` for `llm_token_quota_monthly` and `api_request_budget_monthly`. The quota enforcer reads this to gate requests without hitting Postgres on every call. If the key is absent, the enforcer falls back to 0 (unlimited, since the ceiling hasn't been established yet).

### DeleteUsageCounters

The `quota-reset` reconciler calls `Cache.DeleteUsageCounters(ctx, limitKey, prevPeriod)` which uses Redis `SCAN MATCH quota:usage:*:{limitKey}:{period} DEL` to atomically clear all tenants' stale usage counters at billing period rollover — no tenant enumeration from Postgres needed.

## Events

### Produced

| Event type | Schema | Trigger | Topic |
|-----------|--------|---------|-------|
| `tenant_entitlements_changed` | `internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json` | Every successful `Recompute` that writes a new snapshot version | `SNS_TOPIC_ENTITLEMENTS_ARN` |

Payload fields: `tenant_id`, `version`, `previous_version`, `cause`, `changed_keys`, `computed_at`. Full schema: `api/asyncapi.yaml` → `TenantEntitlementsChangedPayload`.

Schema governance: `api/asyncapi.yaml` is the design-time source of truth. Schemas in `eventbus/schemas/*.json` are embedded via `//go:embed` in the `ValidatingCodec` and must be pre-registered in Glue Schema Registry (resolved once at startup via `GetSchemaByDefinition`).

### Consumed

| Queue env var | Queue name | Consumer struct | Event types handled |
|---|---|---|---|
| `SQS_BILLING_ENTITLEMENTS_QUEUE_URL` | `billing-entitlements-q` | `BillingEventConsumer` | `TenantPlanChanged`, `TenantSubscriptionCancelled`, `TenantReactivated` → Recompute; `TenantQuotaAddOnChanged{reason:purchase}` → ApplyBillingPurchase (E-1); `TenantQuotaAddOnChanged{reason:refund}` → ApplyBillingRefund (C-5 / BL-OQ-23) |
| `SQS_TENANT_ENTITLEMENTS_QUEUE_URL` | `tenant-entitlements-q` | `TenantEventConsumer` | `TenantIdentityProvisioned`, `TenantSubscriptionUpdated` → Recompute; `TenantMembershipsPurged`, `TenantConverted`, `TenantOffboarded` → handleOffboard (Recompute + optional PurgeTrial) |
| `SQS_CATALOG_ENTITLEMENTS_QUEUE_URL` | `catalog-entitlements-q` | `CatalogConsumer` | `PlanCatalogChanged` → InvalidatePlan cache + Temporal fan-out |
| `SQS_MEMBERSHIP_ENTITLEMENTS_QUEUE_URL` | `membership-entitlements-q` | `MembershipConsumer` | `TenantMembershipsPurged` → Recompute; `TenantStateChanged{cause:TrialReactivated}` → Recompute (E-3 read-race fix; published after O&M commit via transactional outbox, ensuring I-17 reflects post-revert state) |

Each queue has a `-dlq` with `maxReceiveCount = 5` RedrivePolicy. `platform-events` forwards at `SQS_MAX_RECEIVE_COUNT` 4 (one below the RedrivePolicy). Idempotency via `processed_events` / `inbox.Store.Process`.

**All consumers:**
- Dedup: `processOnce` / `ackUnknown` from `consumer/dedup.go`; `InboxRepository.ProcessOnce` claims and applies in one tx.
- DLQ: via `events.WithDLQForwarding` in `sqspipeline.NewConsumer`.
- Schema validation: `sqspipeline.Handler` validates inbound payloads against `ValidatingCodec`; rejects with `consumed_event_rejected_total{reason="schema_violation"}` and forwards to DLQ.
- Unknown event types: acked via `ackUnknown` (forward-compat); counted in `unknown_event_acknowledged_total`.

## Event schema registration

Every new event type produced by this service needs:
1. A JSON Schema Draft-07 file in `internal/adapter/outbound/eventbus/schemas/`.
2. An entry in `api/asyncapi.yaml`.
3. Pre-registration in AWS Glue Schema Registry (the `GlueCodec` resolves the version UUID by definition at startup — a missing registration is a startup failure, not a runtime error).
