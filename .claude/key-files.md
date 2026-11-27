# Key Files to Know

Critical implementation files for `commercial-entitlements`. See also [`architecture.md`](architecture.md) for directory tree and layer rules.

- **`cmd/server/main.go`** — composition root: pool setup (`pgcommon.NewPool` with `GUCProvider = GUCSetFromContext` → transaction-local `app.tenant_id` binding on every checkout, RLS-6), middleware, all 4 SQS consumers, Temporal starter, outbox runner, audit runner, graceful shutdown. `buildAudit` wires platform-audit outbox mode. Schema migrations run here unless `RUN_MIGRATIONS=false`. **5 CronJobs** dispatched by `cmd/reconciler --job=<name>`; there are no business-metric exporter goroutines in `cmd/server` (unlike IAM).

- **`cmd/reconciler/main.go`** — single-binary CronJob dispatcher; `--job=<name>` (or `RECONCILER_JOB`); 5 jobs: `override-expiry`, `addon-expiry`, `outbox-prune`, `processed-events-prune`, `quota-reset`. Wires Valkey for `quota-reset` via `VALKEY_URL`. `RECONCILER_TIMEOUT` 5m, `RECONCILER_STATEMENT_TIMEOUT` 2m. Prometheus Pushgateway push after each run.

- **`internal/core/domain/entitlement.go`** — `EffectiveSet`, `EntitlementState` ENUM (`active`, `trial`, `grace_period`, `suspended`, `offboarded`, `provisioning`), `LimitKey` constants (`llm_token_quota_monthly`, `api_request_budget_monthly`, `api_rate_limit_rps`, `workflow_template_limit`, `tender_limit`), `LimitValue{Value *int64}` (nil = unlimited), `KnownLimitKeys` map for validation.

- **`internal/core/service/effective_set.go`** — most important service. `Recompute` runs the 5-step merge pipeline: (1) plan baseline from `CatalogService`, (2) trial-profile limit overrides if `state == trial`, (3) trial entitlement overrides if `state == trial`, (4) operator overrides (non-expired), (5) additive quota addon balance. **ENT-INV-6 no-op guard**: `effectiveSetUnchanged` compares computed set against latest snapshot — if identical, skips the write and event emission. `writeQuotaCeiling` writes ENT-D13 quota ceiling key to Valkey after every snapshot write.

- **`internal/core/service/addon.go`** — `AddonService`. `RecordDrawdown` checks `DrawdownRepository.GetByRequestID` **before** inserting (ENT-INV-3 idempotency). FIFO drawdown order: `ListActiveByTenantAndKey` returns addons sorted by `expires_at ASC NULLS LAST, created_at ASC`. Shortfall raised when `available < requested` after exhausting all active addons.

- **`internal/core/service/catalog.go`** — `CatalogService`. Read-through cache over `IAMClient` + `CatalogCacheRepository`. Calls `IAMClient.FetchPlan` on a cache miss and stores result in `plan_catalog_cache`. On a `PlanCatalogChanged` event, invalidates both Postgres cache and Valkey `ent:plan:{code}` key, then optionally fans out via Temporal workflow. **NOT fail-open** — a cold Postgres cache + IAM outage → 503; this is the only fail-closed upstream dependency.

- **`internal/core/port/outbound.go`** — all outbound port interfaces: `SnapshotRepository`, `OverrideRepository`, `AddonRepository`, `DrawdownRepository`, `ShortfallRepository`, `TrialRepository`, `CatalogCacheRepository`, `TxRunner`, `Cache` (with `DeleteUsageCounters`), `IAMClient` (`FetchPlan`, `FetchAllPlans`, `FetchTenantSubscription`), `TenantSubscription` (JSON-tagged to I-17 wire shape; `FeatureFlags []string`), `Inbox`, `EventPublisher`, `TemporalStarter`, `AuditRecorder`.

- **`internal/core/port/inbound.go`** — service interfaces: `EffectiveSetService`, `OverrideService`, `AddonService`, `ShortfallService`, `TrialService` and all request types (SetOverrideRequest, AddAddonRequest, RecordDrawdownRequest, SetTrialRequest, etc.).

- **`internal/adapter/outbound/postgres/db.go`** — `withPool(ctx, pool, fn)` — the low-level helper every repository uses; reads the active `pgx.Tx` from context (injected by `TxRunner.RunInTx`) or falls back to a fresh pool connection. `TxRunner.RunInTx` opens a transaction, stores it in ctx via `port.WithTx`, injects `port.WithEventPublisher`, commits; also injects `port.AuditRecorderFromContext` when `WithAuditRecorder` is configured. `wrapConnErr` maps SQLSTATE classes.

- **`internal/adapter/outbound/postgres/addon_repository.go`** — `AddonRepository` + `DrawdownRepository`. Addon `Update` checks `record_version` in WHERE and returns `ErrOptimisticLockConflict` on 0 rows affected (CONC-2). `ListActiveByTenantAndKey` orders by `expires_at ASC NULLS LAST` for FIFO drawdown.

- **`internal/adapter/outbound/postgres/snapshot_repository.go`** — `SnapshotRepository.Upsert` uses `ON CONFLICT (tenant_id) DO UPDATE SET ... WHERE entitlement_snapshots.version < EXCLUDED.version` to enforce ENT-INV-1 (monotonic version — a concurrent stale recompute never overwrites a newer snapshot).

- **`internal/adapter/outbound/valkey/cache.go`** — advisory Valkey cache; all key builder functions and TTL constants; `DeleteUsageCounters(ctx, limitKey, period)` uses Redis `SCAN+DEL` pattern. All errors are **returned to callers who choose to ignore them** — never fail-closed (CACHE-1).

- **`internal/adapter/outbound/eventbus/publisher.go`** — `Publisher.Enqueue` writes to `outbox_events` via `outbox.EnqueueOrdered`; reads `pgx.Tx` from ctx via `port.TxFromContext` (or `port.EventPublisherFromContext`) — the tx is always started by `TxRunner.RunInTx`, never by this adapter. Schema validation via `ValidatingCodec` on enqueue. **Only call site for `outbox.EnqueueOrdered`** — no other file may call it directly (arch-lint + CI check).

- **`internal/adapter/outbound/iamclient/client.go`** — Single HTTP client implementing `port.IAMClient` for two upstream IAM services. `FetchPlan`/`FetchAllPlans` call `{IAM_CATALOG_BASE_URL}/api/v1/internal/plans[/:code]` (iam-catalog-admin, CAT-I2). `FetchTenantSubscription` calls `{IAM_ORG_BASE_URL}/api/v1/internal/tenants/:id/plan-context` (iam-org-membership, I-17). Both use Istio mTLS — no API key. `FetchAllPlans` is used by `cmd/catalog-refresh` for cold-start bootstrap. Built via `otelhttp.NewTransport` (OBS-9). Timeout: `IAM_CATALOG_TIMEOUT` (default 10 s).

- **`internal/adapter/inbound/consumer/dedup.go`** — `processOnce`/`ackUnknown` helpers. `processOnce` calls `inbox.ProcessOnce(ctx, consumerName, eventID, fn)` which claims the event in `processed_events` as the first statement of `fn`'s transaction — a concurrent duplicate blocks on the row lock and is then skipped (IDEMP-1..4).

- **`internal/adapter/inbound/consumer/catalog_consumer.go`** — `CatalogConsumer`. Handles `PlanCatalogChanged` → invalidates `plan_catalog_cache` row + Valkey `ent:plan:{code}` + triggers `TemporalStarter.StartCatalogFanout` (if wired). Unknown types acked via `ackUnknown`.

- **`internal/adapter/temporal/catalog_fanout_workflow.go`** — Temporal workflow. Fan-out strategy: child-workflow per tenant on the plan, or single workflow iterating over all tenants. Calls `RecomputeActivity` per tenant.

- **`internal/adapter/inbound/http/router.go`** — single source of truth for the full route table. Register new routes here and in the corresponding handler file. All routes under `/internal/v1/`.

- **`internal/adapter/inbound/http/middleware.go`** — `RequireActiveTenant`, `RequireOperatorRole`, `HandleError` (maps `domain.DomainError` sentinels → HTTP status + error code).

- **`internal/adapter/outbound/postgres/migrations/000001_initial_schema.up.sql`** — full initial schema: all tables, ENUMs, RLS policies, triggers, grants in one consolidated migration. The authoritative source for the data model.

- **`internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json`** — schema for the one produced event type. Add new files here for new event types.

- **`api/asyncapi.yaml`** — design-time source of truth for the entitlements event contract.

- **`docs/lld/commercial-lld-entitlements-service.md`** — authoritative LLD spec. All invariant IDs (ENT-INV-*, CONS-*, EVT-*, RLS-*) reference this document.

- **`deploy/helm/templates/cronjobs.yaml`** — 5 CronJobs dispatched by `cmd/reconciler --job=<name>`. CronJob names must match the registry keys in `cmd/reconciler/main.go`.

- **`cmd/reconciler/jobs/quota_reset.go`** — `QuotaReset` job. Uses `Cache.DeleteUsageCounters` SCAN+DEL to clear `quota:usage:*:{limitKey}:{prevPeriod}` from Valkey without enumerating tenant IDs. Advisory: `jctx.Cache == nil` skips gracefully.

- **`.go-arch-lint.yml`** — dependency boundary rules (enforced in CI). Key rule: `internal/core/*` must not import `internal/adapter/*` except `core/service` → `adapters_outbound_metrics`.
