# Enterprise Platform Observability Standard — commercial-entitlements

How this service implements the standard defined and enforced by [platform-gincommon](https://github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/tree/main/docs/observability). This page covers what is specific to commercial-entitlements; the standard itself, its rules and the governance process are documented there.

| Piece | Location |
|---|---|
| Metric inventory (every metric this service emits, by tier) | [`metric-registry.md`](metric-registry.md) |
| Identity (`domain`, `service`, `environment`) | `cmd/server/main.go` → `gincommon.Config{Domain: "commercial", ServiceName: envOr("APP_NAME", "commercial-entitlements")}` |
| Collector construction and registration | `internal/adapter/outbound/metrics/registry.go` (`Register`) |
| Compliance checks in CI | `.github/scripts/metricslint.sh`, `check-metric-naming.sh`, `metrics-registry-lint.sh` |
| Alerts, recording rules, SLOs | `deploy/monitoring/app-alerts.yml`, `slo-rules.yml`, `platform-audit-rules.yml` |
| Dashboards, HPA | `deploy/monitoring/dashboards/`, `deploy/monitoring/prometheus-adapter-rule.yaml` |
| Renamed and removed metric names | [`migration.md`](migration.md) |
| Alert runbooks | [`runbooks.md`](runbooks.md) |

## Platform libraries

| Library | Version | Emits here | Wiring |
|---|---|---|---|
| platform-gincommon | v2 | `platform_http_*`, `platform_build_info`, `platform_library_info` | `gincommon.ObservabilityMiddlewares(cfg)` in `http/router.go`; `/metrics` on `METRICS_PORT` via `gincommon.MetricsHandler()` |
| platform-events/v2 | v2 | `platform_messages_*`, `platform_retry_total`, `platform_dlq_messages_total`, `platform_duplicate_messages_total`, `platform_outbox_*`, `platform_dependency_request_seconds` | `events.InitMetrics(events.MetricsIdentityFromLabels(gincommon.MetricsConstLabels()), gincommon.MetricsRegisterer())` |
| platform-pgcommon/v2 | v2 | `platform_db_*` — `pool="default"` (app pool) and `pool="system"` (BYPASSRLS pool) | `pgmetrics.InitWithIdentity(pgmetrics.IdentityFromLabels(...), gincommon.MetricsRegisterer())` |
| platform-audit | v0.1 | `platform_audit_*` (audit-outbox backlog, delivery outcomes and lag, runner liveness) | `audit.InitMetrics(...)` in `cmd/server/main.go`; alerts and rules in `deploy/monitoring/platform-audit-rules.yml` |

All four register on `gincommon.MetricsRegisterer()` — one scrape serves every collector. Registration order in `cmd/server/main.go`: `ObservabilityMiddlewares` → `metrics.Register` → `events.InitMetrics` → `pgmetrics.InitWithIdentity` → `audit.InitMetrics` → pools.

## Identity

| Label | Value | Source |
|---|---|---|
| `domain` | `commercial` | `gincommon.Config.Domain` (hardcoded in `cmd/server/main.go`) |
| `service` | `commercial-entitlements` | `APP_NAME`, default `commercial-entitlements`, in **both** binaries |
| `environment` | `local`, `dev`, `test`, `staging` or `prod` | `resolveAppEnv()`: `APP_ENV`, else `dev`; passed to metrics, tracing, logging |
| `version` | build version | `BUILD_VERSION`; only on `platform_build_info` |

Reconciler CronJobs share the server's identity. Each run pushes its metrics to `PROMETHEUS_PUSHGATEWAY_URL`, grouped by `cronjob`. CronJobs are never scraped.

## Metric tiers

| Tier | Namespace | Built by | Count |
|---|---|---|---|
| 1 — platform-shared | `platform_*` | Platform libraries | see [inventory](metric-registry.md) |
| 3 — service-specific | `commercial_entitlements_*` | `internal/adapter/outbound/metrics/registry.go` via `gincommon.MetricsRegisterer()` | 14 |

This service has **no Tier 2 domain-shared metrics** (no `commercial_*` shared collectors exist yet).

### Label vocabulary (Tier 3)

- `cause` ∈ `domain.EffectiveSetCause` values (string enum defined in `domain/entitlement.go`)
- `outcome` ∈ `computed`, `noop`, `error`
- `limit_key` ∈ bounded set of `domain.LimitKey` values
- `disposition` ∈ `domain.ShortfallDisposition` string values
- `status` ∈ `auto_accepted`, `pending_approval`
- `shortfall` ∈ `yes`, `no`
- `kind` ∈ `effective_set`, `plan`
- `consumer` ∈ stable queue-name strings (`billing-entitlements-q`, `tenant-entitlements-q`, etc.)
- `event_type` ∈ platform event type string

High-cardinality identifiers (`tenant_id`, `user_id`, `request_id`, `event_id`) are **prohibited** by the standard and absent here.

## Adding a metric

1. Decide the tier with the standard's decision tree.
2. Tier 3: add the collector to `register()` in `registry.go` using `gincommon.MetricsRegisterer()` as the registerer. Name: `commercial_entitlements_<name>` with `_total` (counter), `_seconds` (histogram), no suffix (gauge). Snake_case labels, no `*_id`/`email`/`ip` labels.
3. Add a nil-safe helper in `business.go`.
4. Record one representative sample in `TestStandard_LibrariesShareRegistryCollectors`.
5. If it backs an alert, add the runbook entry to [`runbooks.md`](runbooks.md).

## CI enforcement

All in `Validate / Quality` (`.github/workflows/validate-quality.yml`):

| Check | What it enforces |
|---|---|
| `check-metric-naming.sh` | Every metric in `internal/adapter/outbound/metrics/` has `commercial_entitlements_*` or `platform_*` prefix; counter `_total`; histogram `_seconds` |
| `metrics-registry-lint.sh` | No duplicate names; prefix / suffix rules via `metrics-inventory.py` |
| `metricslint.sh` | Full metricslint suite via platform-gincommon |

## SLOs

Recording rules and burn-rate alerts in `deploy/monitoring/slo-rules.yml`:

| SLO | SLI | Target | Alerts |
|---|---|---|---|
| SLO-1 Recompute hot-path availability | Write-path error ratio on `/api/v1/tenants/*` in `platform_http_requests_total` | 99.9% non-5xx | `CommercialEntitlementsSLOWriteErrorBudgetFastBurn` (14.4×, page), `CommercialEntitlementsSLOWriteErrorBudgetSlowBurn` (6×, ticket) |

## HPA

`deploy/monitoring/prometheus-adapter-rule.yaml` exposes `platform_http_requests_per_second` (from `platform_http_requests_total`) as the optional RPS-per-replica HPA input; CPU and memory remain the primary signals.
