# Renamed and removed metric names

Records every name change or removal so `metricslint refs` can catch stale references in alerts, dashboards, and runbooks.

This service was built against the Enterprise Platform Observability Standard from the start, so the history here starts at v0.1.0. Any future rename or removal should be recorded below.

## Schema (per entry)

| Column | Meaning |
|---|---|
| Before | The old metric name (or `—` if newly added) |
| After | The new name (or `removed` if dropped) |
| Why | Reason for the change |
| Since | First release where the new name applies |

## Changes

| Before | After | Why | Since |
|---|---|---|---|
| — | *This table is empty — no renames or removals yet.* | — | — |

## Adding a new entry

When you rename or remove a metric:

1. Add a row to the table above.
2. Update every reference in `deploy/monitoring/app-alerts.yml`, `slo-rules.yml`, `deploy/monitoring/dashboards/*.json`, and `docs/observability/runbooks.md`.
3. Run `make metrics-inventory` to regenerate [`metric-registry.md`](metric-registry.md).
4. CI's `metricslint refs` will fail on any dangling reference in `deploy/` or `docs/`.
