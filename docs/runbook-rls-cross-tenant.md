# Runbook: RLS Cross-Tenant Access Violation

**Alert:** `CommercialEntitlementsRLSCrossTenantAccess`
**Severity:** Critical — zero tolerance. Any cross-tenant data leak pages immediately.

## Symptoms

- `commercial_entitlements_rls_violations_total{violation_type="cross_tenant_access"}` > 0
- Structured logs: `violation_type=cross_tenant_access` with `tenant_id`, `query_text`, `session_role`

## Immediate Actions

1. Query violation log immediately:
   ```sql
   SELECT tenant_id, session_role, client_addr, query_text, occurred_at
   FROM rls_violation_log
   WHERE violation_type = 'cross_tenant_access'
   ORDER BY occurred_at DESC
   LIMIT 10;
   ```
2. Identify which tenant's data was accessed and by which GUC-bound tenant.
3. If confirmed cross-tenant data exposure: **page on-call immediately, consider pod restart** to clear any in-flight requests.
4. Check recent deployments for GUC binding changes in `cmd/server/` or `pkg/requestctx/`.

## Root Causes

- Missing `pgcommon.GUCSetFromContext` on a repository path that bypasses the standard `RunInTx` wrapper.
- A `sysPool` (BYPASSRLS) query path where a tenant-scoped query was incorrectly routed.
- A new repository function that calls `WithConn` without first setting the GUC via `pgcommon.WithGUCSet`.

## Resolution

- **Short-term:** Restart affected pods to clear any cached connections.
- **Long-term:** Trace the query path in `internal/adapter/outbound/postgres/` and confirm `pgcommon.GUCSetFromContext` is wired for every tenant-scoped repository function.
- Add a test that calls the offending function and asserts the GUC is set.
- CI enforces `RLS-6` via `Reject non-LOCAL SET app.tenant_id` check in `.github/scripts/check-database-access.sh`.

## Invariants

- **RLS-1:** RLS enabled + forced on every tenant-scoped table.
- **RLS-6:** `app.tenant_id` GUC is `SET LOCAL`, never session-scoped. Never leaks across PgBouncer pooled backends.
