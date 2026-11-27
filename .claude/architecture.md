# Architecture

Clean Architecture / Ports-and-Adapters — dependencies point inward; `core/domain` imports nothing external; `cmd/server/main.go` (HTTP API + SQS consumers), `cmd/reconciler/main.go` (CronJob dispatcher), `cmd/worker/main.go` (Temporal worker), `cmd/migrate/main.go` (schema migration), `cmd/expiry/main.go` (addon/override sweep), and `cmd/catalog-refresh/main.go` (IAM plan catalog pull) are the six composition roots.

## Package Layout

```
commercial-entitlements/
├── cmd/
│   ├── server/
│   │   ├── main.go           # HTTP + SQS + outbox runner + audit runner; wires all adapters;
│   │   │                     # pool setup with GUCProvider = GUCSetFromContext (RLS-1)
│   │   ├── wiring.go         # buildTopicPublisher / buildTopicCodec (SNS + Glue codec per topic)
│   │   ├── config.go         # resolveAppEnv, isDevLikeAppEnv, envOr, envInt, loggerEnv
│   │   ├── identity.go       # APP_ENV/ENVIRONMENT resolution helper
│   │   └── swagger_info.go   # swag annotation entry point for `make swag`
│   ├── reconciler/
│   │   ├── main.go           # CronJob dispatcher; --job=<name> (or RECONCILER_JOB);
│   │   │                     # RECONCILER_TIMEOUT 5m; wires Valkey for quota-reset
│   │   ├── identity.go
│   │   └── jobs/
│   │       ├── jobs.go       # jobs.Func, jobs.Result, jobs.Context (all deps)
│   │       ├── override_expiry.go
│   │       ├── addon_expiry.go
│   │       ├── outbox_prune.go
│   │       ├── processed_events_prune.go
│   │       └── quota_reset.go  # SCAN+DEL usage counters (ENT-D13)
│   ├── worker/
│   │   └── main.go           # Temporal worker for catalog fan-out workflow
│   ├── migrate/
│   │   └── main.go           # pgadapter.MigrateSchema; run once per release by Helm pre-upgrade Job
│   ├── expiry/
│   │   └── main.go           # one-shot addon/override expiry sweep
│   └── catalog-refresh/
│       └── main.go           # pulls plan catalog from IAM and warms Postgres + Valkey cache
├── internal/
│   ├── core/
│   │   ├── domain/           # entities, value objects, DomainError; NO external deps
│   │   │   ├── entitlement.go  # EffectiveSet, EntitlementState, LimitKey, LimitValue, KnownLimitKeys
│   │   │   ├── plan.go         # Plan, TrialProfileLimits, ModelTier
│   │   │   ├── addon.go        # QuotaAddon, QuotaAddonDrawdown, QuotaAddonStatus
│   │   │   ├── override.go     # Override, OverrideHistory
│   │   │   ├── shortfall.go    # Shortfall, ShortfallStatus, ShortfallDisposition
│   │   │   ├── snapshot.go     # EntitlementSnapshot, EffectiveSetCause constants
│   │   │   ├── trial.go        # TenantTrialAssignment, TrialEntitlementOverride, TrialProfile
│   │   │   ├── event.go        # DomainEvent, EventType, TenantEntitlementsChangedPayload,
│   │   │   │                   # InboundBillingPlanChangedEvent, InboundBillingSubscriptionCancelledEvent, InboundBillingReactivatedEvent, InboundTenantEvent, InboundMembershipEvent,
│   │   │   │                   # InboundTenantStateChangedEvent (EVT-10)
│   │   │   ├── event_helpers.go # NewTenantEvent constructor
│   │   │   └── errors.go       # ErrSnapshotNotFound, ErrAddonNotFound, ErrOverrideNotFound,
│   │   │                       # ErrShortfallNotFound, ErrTrialAssignmentNotFound, ErrPlanNotFound,
│   │   │                       # ErrTenantNotFound, ErrOptimisticLockConflict, ErrInvalidLimitKey
│   │   ├── port/             # interfaces required by the core
│   │   │   ├── inbound.go      # EffectiveSetService, OverrideService, AddonService,
│   │   │   │                   # ShortfallService, TrialService + all request types
│   │   │   ├── outbound.go     # SnapshotRepository, OverrideRepository, AddonRepository,
│   │   │   │                   # DrawdownRepository, ShortfallRepository, TrialRepository,
│   │   │   │                   # CatalogCacheRepository, TxRunner, Cache, IAMClient,
│   │   │   │                   # Inbox, EventPublisher, TemporalStarter, AuditRecorder
│   │   │   ├── context.go      # EventPublisherFromContext, WithEventPublisher
│   │   │   ├── logger.go       # Logger, SlogStyleLogger, NopLogger, NewSlogStyleLogger
│   │   │   └── trace.go        # SpanContext, SetTraceExtractor, SetConsumerSpanFunc
│   │   └── service/          # use cases; may import only metrics adapter
│   │       ├── effective_set.go  # Recompute 5-step merge pipeline; ENT-INV-6 no-op guard
│   │       ├── catalog.go        # CatalogService — read-through over IAMClient + cache + Temporal
│   │       ├── addon.go          # AddonService — drawdown idempotency (ENT-INV-3), FIFO drawdown
│   │       ├── override.go       # OverrideService
│   │       ├── trial.go          # TrialService
│   │       └── helpers.go        # validateLimitKey
│   └── adapter/
│       ├── inbound/
│       │   ├── http/             # Gin handlers, DTOs
│       │   │   ├── router.go           # single source of truth for route table
│       │   │   ├── dto.go              # request/response shapes
│       │   │   ├── errors.go           # ErrorResponse helpers
│       │   │   ├── middleware.go       # auth, tenant-scoping, request logging
│       │   │   ├── entitlement_handler.go  # GetEffectiveSet, Recompute
│       │   │   ├── override_handler.go     # Set/Remove/Get/List/History/Expire
│       │   │   ├── addon_handler.go        # Add/Remove/List/Drawdown
│       │   │   ├── shortfall_handler.go    # List/Resolve shortfalls
│       │   │   ├── trial_handler.go        # SetTrial/Override/GetAssignment/Purge
│       │   │   └── plans_handler.go        # GetPlan, ListPlans, RefreshCatalog
│       │   ├── consumer/
│       │   │   ├── billing_consumer.go     # billing-entitlements-q
│       │   │   ├── tenant_consumer.go      # tenant-entitlements-q
│       │   │   ├── catalog_consumer.go     # catalog-entitlements-q (cache invalidation + Temporal fan-out)
│       │   │   ├── membership_consumer.go  # membership-entitlements-q: TenantMembershipsPurged,
│       │   │   │                           #   TenantStateChanged (TrialReactivated E-3; Billing causes B-3; EVT-10)
│       │   │   └── dedup.go               # processOnce / ackUnknown over port.Inbox
│       │   └── sqspipeline/
│       │       └── consumer.go            # EnvForQueue defaults, NewConsumer (+GlueDecodeCodec, B-1), Handler, RequireDLQ
│       ├── outbound/
│       │   ├── postgres/
│       │   │   ├── db.go                  # withPool, TxRunner (+ WithAuditRecorder), wrapConnErr, DSNFromEnv
│       │   │   ├── snapshot_repository.go
│       │   │   ├── override_repository.go
│       │   │   ├── addon_repository.go    # AddonRepository + DrawdownRepository
│       │   │   ├── shortfall_repository.go
│       │   │   ├── trial_repository.go
│       │   │   ├── catalog_cache_repository.go
│       │   │   ├── idempotency_repository.go  # InboxRepository (platform-events inbox.Store)
│       │   │   ├── migrate.go             # MigrateSchema (outbox.ApplySchema then domain migrations)
│       │   │   ├── logger_adapter.go
│       │   │   └── migrations/            # 000001_initial_schema.{up,down}.sql
│       │   ├── valkey/
│       │   │   └── cache.go               # all cache key patterns, TTLs, DeleteUsageCounters
│       │   ├── eventbus/
│       │   │   ├── publisher.go           # transactional outbox Enqueue
│       │   │   ├── codec.go               # NoopCodec, EventSource constant
│       │   │   ├── glue_codec.go          # GlueCodec — startup schema resolution via GetSchemaByDefinition
│       │   │   ├── validating_codec.go    # ValidatingCodec (embedded schemas/*.json)
│       │   │   └── schemas/               # one JSON Schema Draft-07 per event type (//go:embed)
│       │   ├── iamclient/
│       │   │   └── client.go              # IAM_CATALOG_BASE_URL (iam-catalog-admin: FetchPlan, FetchAllPlans) +
│       │   │                              #   IAM_ORG_BASE_URL (iam-org-membership: FetchTenantSubscription); Istio mTLS, no API key
│       │   ├── auditrecorder/
│       │   │   └── recorder.go            # platform-audit outbox mode (pending_audit_entries on tx)
│       │   └── metrics/
│       │       ├── registry.go            # all prometheus.CounterVec/HistogramVec instances
│       │       └── business.go            # Inc* helpers called from service and consumer layers
│       └── temporal/
│           ├── activities.go              # catalog fan-out activity (call EffectiveSetService.Recompute per tenant)
│           ├── catalog_fanout_workflow.go # Temporal workflow for plan-catalog change fan-out
│           └── starter.go                # TemporalStarter adapter
├── api/
│   ├── asyncapi.yaml                      # AsyncAPI 3.0 — commercial.entitlements.events channel
│   └── embed.go
├── deploy/
│   └── helm/
│       ├── Chart.yaml
│       ├── templates/
│       │   ├── deployment.yaml
│       │   ├── cronjobs.yaml              # 5 CronJobs dispatched by cmd/reconciler
│       │   ├── migrate-job.yaml           # Helm pre-upgrade hook
│       │   └── service.yaml
│       ├── values.yaml
│       ├── values-dev.yaml
│       └── values-prod.yaml
├── docs/swagger/                          # generated by `make swag` from handler annotations
├── Dockerfile  docker-compose.yml  Makefile  go.mod  .golangci.yml  .go-arch-lint.yml
```

## Shared Library Dependencies

```go
require (
    github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2   v2.x.x
    github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2      v2.x.x
    github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2    v2.x.x
    github.com/BCBP-SOLUTIONS-FZC-LLC/platform-audit          v0.x.x
    go.temporal.io/sdk                                         v1.30.x
)
```

**`platform-audit` (outbox mode):** AL-5 Audit Log entries are enqueued by `auditoutbox.Enqueuer` into `pending_audit_entries` on the business transaction (`port.AuditRecorder`, injected by `postgres.TxRunner.WithAuditRecorder`) and delivered at-least-once by `auditoutbox.Runner` on the sysPool. `postgres.MigrateSchema` runs `auditoutbox.ApplySchema` after `outbox.ApplySchema`.

**`platform-gincommon` integration:** `NewRouter` chains `gincommon.ObservabilityMiddlewares` (`PanicRecovery → RequestID → Tracing → CorrelationHeaders → Metrics → Logging → Timeout`, 30s). `gincommon.ProtectedMiddlewares` applies JWT auth on the `/internal/v1` group. `ServiceName` from `APP_NAME` (default `commercial-entitlements`). `/metrics` on `METRICS_PORT` via `gincommon.MetricsHandler()`.

**`platform-pgcommon` integration:**
- `NewPool` + `ConfigFromEnv`. `GUCProvider = GUCSetFromContext` — binds `app.tenant_id` **transaction-locally** (`SET LOCAL`) on every checkout. Never session-scoped (RLS-1/RLS-6).
- Writes in `pgcommon.RunInTx` via `TxRunner`; consumer dedup in `inbox.Store.Process`'s own tx.
- Error mapping: `IsUniqueViolation`, `IsDeadlock`, `IsSerializationFailure`, `IsPoolClosed`.
- System pool (`SYSTEM_DATABASE_URL`) used by reconciler's cross-tenant sweeps (BYPASSRLS).

**`platform-events` integration:**
- **Publish:** single-topic `events.Publisher` via `events.NewSNSPublisher` wired to `SNS_TOPIC_ENTITLEMENTS_ARN` (`SQS_*_ENTITLEMENTS_QUEUE_URL` for each consumer).
- **Enqueue:** `eventbus.Publisher.Enqueue` writes to `outbox_events` via `outbox.EnqueueOrdered`; never a hand-rolled INSERT.
- **Consume:** every SQS consumer built via `events.NewSQSConsumerWithClient` with `events.GlueDecodeCodec{}` + `events.WithDLQForwarding`. Consumer dedup via `processed_events` / `inbox.Store.Process`.
- **Prune:** `outbox.Runner.PrunePublished` (outbox-prune job) and `inbox.Store.Prune` (processed-events-prune job).

## Dependency Rules (`go-arch-lint`, `.go-arch-lint.yml`)

| Component | May depend on |
|-----------|---------------|
| `core_domain` | nothing |
| `core_port` | `core_domain` |
| `core_service` | `core_domain`, `core_port` |
| `adapters_inbound_http` | `core_domain`, `core_port` |
| `adapters_inbound_consumer` | `core_domain`, `core_port`, `adapters_outbound_metrics` |
| `adapters_inbound_sqspipeline` | `core_port` |
| `adapters_outbound_postgres` | `core_domain`, `core_port` |
| `adapters_outbound_valkey` | `core_domain`, `core_port` |
| `adapters_outbound_eventbus` | `core_domain`, `core_port` |
| `adapters_outbound_metrics` | `core_domain` |
| `adapters_outbound_auditrecorder` | `core_port` |
| `adapters_outbound_iamclient` | `core_domain`, `core_port` |
| `adapters_temporal` | `core_domain`, `core_port`, `core_service` |
| `cmd` | everything except cross-adapter imports |

**Critical rule:** nothing in `internal/core/*` may import `internal/adapter/*` except `core/service` → `adapters_outbound_metrics` (business-outcome counters at point of outcome — sanctioned in `.go-arch-lint.yml`).

## Temporal Fan-out

`CatalogFanoutWorkflow` runs when a plan definition changes in the IAM catalog:
1. `CatalogConsumer` receives `PlanCatalogChanged` event → invalidates Postgres plan cache + Valkey plan key.
2. If `TemporalStarter` is wired (`TEMPORAL_HOST_PORT` set), it triggers `CatalogFanoutWorkflow`.
3. The workflow executes one `RecomputeActivity` per tenant on that plan.
4. Each activity calls `EffectiveSetService.Recompute(ctx, tenantID, CauseCatalogRefresh)`.
Temporal is optional — when `TEMPORAL_HOST_PORT` is unset, the fan-out is skipped (catalog refresh degrades gracefully to on-demand recomputation on next read).
