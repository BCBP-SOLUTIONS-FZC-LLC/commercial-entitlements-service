# Runbook: Recompute Latency

**Alert:** `CommercialEntitlementsHighLatency`, `CommercialEntitlementsSLOWriteErrorBudgetFastBurn`
**SLO:** p99 < 500ms for recompute operations; p99 < 2s across all routes.

## Symptoms

- `commercial_entitlements_recompute_duration_seconds` p99 above budget
- `platform_http_request_duration_seconds` p99 above 2s
- `commercial_entitlements_cache_miss_total{kind="effective_set"}` rate elevated (cache-miss flood)

## Immediate Actions

1. Check cache hit rate — `rate(commercial_entitlements_cache_hit_total{kind="effective_set"}[5m]) / (rate(commercial_entitlements_cache_hit_total{kind="effective_set"}[5m]) + rate(commercial_entitlements_cache_miss_total{kind="effective_set"}[5m]))`. A falling ratio means Valkey eviction or cold cache.
2. Check Postgres slow queries — `platform_db_slow_queries_total{pool="default"}` and slow-query log (`PG_SLOW_QUERY_THRESHOLD=200ms`).
3. Check pool saturation — `platform_db_pool_empty_acquires_total`.
4. Check if a consumer event storm is triggering mass recomputes — `sum by (cause) (rate(commercial_entitlements_recompute_total[5m]))`.

## Root Causes

- **Cache miss flood:** Valkey eviction under memory pressure, or a mass-invalidation event (catalog change, plan update) causing all tenants to miss simultaneously. Mitigate: scale Valkey memory, or stagger invalidation.
- **Slow Postgres query:** Missing index, table bloat, or lock contention on `entitlement_snapshots`. Run `EXPLAIN ANALYZE` on the snapshot lookup query.
- **Pool exhaustion:** Too few connections for concurrent recomputes. Increase `PG_MAX_CONNS` or scale pods.
- **Consumer storm:** Billing + membership events arriving in bursts triggering many simultaneous recomputes. ENT-INV-6 `noop` path is cheap; ENT-INV-1 version check ensures only the first writer wins (CONC-1 optimistic lock). Check `commercial_entitlements_recompute_total{outcome="noop"}` rate.

## Resolution

- Increase Valkey memory limit or tune TTLs (default 300s for effective-set cache).
- Verify `entitlement_snapshots` has an index on `(tenant_id, snapshot_version DESC)`.
- Scale pods via HPA (`autoscaling.targetRPSPerReplica`, or bump `minReplicas`).
- For plan catalog miss: check `IAM_CATALOG_BASE_URL` is reachable and the catalog-refresh CronJob ran recently.
