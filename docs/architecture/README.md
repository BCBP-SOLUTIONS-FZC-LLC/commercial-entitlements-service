# Architecture diagrams

Standalone Mermaid source files. Each `.mmd` file is the single source of truth for its diagram.

To render locally, open any `.mmd` file in a Mermaid-aware IDE (VS Code + Mermaid Preview, IntelliJ + Mermaid plugin) or paste into [mermaid.live](https://mermaid.live).

| File | Diagram | Related section |
|------|---------|----------------|
| [`layer-model.mmd`](mermaid/layer-model.mmd) | Clean Architecture layer graph — the six binaries (`cmd/server`, `cmd/reconciler`, `cmd/worker`, `cmd/migrate`, `cmd/expiry`, `cmd/catalog-refresh`), inbound adapters (`http`, `sqspipeline`, `consumer`), outbound adapters (`postgres`, `valkey`, `eventbus`, `iamclient`, `metrics`, `auditrecorder`), core | LLD §3 |
| [`package-dependencies.mmd`](mermaid/package-dependencies.mmd) | Go import graph (mirrors `.go-arch-lint.yml`; inbound → core → outbound; adapters never import each other) | `.go-arch-lint.yml` |
| [`request-flow.mmd`](mermaid/request-flow.mmd) | Full HTTP request lifecycle — recompute hot path: `HTTP handler → EntitlementsService → EntitlementRepository → Postgres`; cache read/write via `ValkeyCache` | LLD §8, CLAUDE.md request-flows.md |
| [`write-flow.mmd`](mermaid/write-flow.mmd) | Transactional write + validated ordered outbox enqueue (`EnqueueOrdered` under advisory lock) → `outbox runner` → SNS topic (GlueCodec) | LLD §7.3 |
| [`data-model.mmd`](mermaid/data-model.mmd) | ER diagram — 10 tables: `entitlement_snapshots`, `entitlement_overrides`, `entitlement_override_history`, `quota_addons`, `quota_addon_drawdowns`, `quota_addon_shortfalls`, `tenant_trial_assignments`, `trial_entitlement_overrides`, `plan_catalog_cache`, `processed_events` | LLD §4, `database-schema.md` |
| [`cache-strategy.mmd`](mermaid/cache-strategy.mmd) | Valkey key namespace, TTLs, read/write/invalidation paths — `ent:tenant:*`, `ent:plans:*`, `quota:tenant:*`, `quota:usage:*` | LLD §6, `api-caching-events.md` |
| [`event-outbox-flow.mmd`](mermaid/event-outbox-flow.mmd) | Outbox enqueue → `outbox runner` → SNS `commercial-entitlements-events` → SQS fan-out; inbound pipeline: 4 SQS consumers (billing, tenant, catalog, membership) → inbox dedup → handler | LLD §7 |
| [`rls-guc-flow.mmd`](mermaid/rls-guc-flow.mmd) | `app.tenant_id` GUC injection per request (`pgcommon.GUCSetFromContext`, `SET LOCAL`) — RLS-6 | LLD §4.3 |
| [`observability-stack.mmd`](mermaid/observability-stack.mmd) | OTel + Prometheus three-tier metrics (`gincommon.MetricsHandler`) + Zap structured logs + reconciler Pushgateway + alert routing | CLAUDE.md, `docs/observability/` |
