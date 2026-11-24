# Database Schema

Database `commercial_entitlements` on RDS PostgreSQL 16, fronted by PgBouncer transaction pooling.
Pool: `pgcommon.NewPool(... GUCProvider: pgcommon.GUCSetFromContext)`.
Local dev uses `postgres:16-alpine` in `docker-compose.yml`.

**Connection-error classification (`wrapConnErr`, `internal/adapter/outbound/postgres/db.go`).**
Every `withPool`/`RunInTx` call passes errors through `wrapConnErr`: a request deadline, `statement_timeout` (57014), or `lock_timeout` (55P03) → 503 `request_timeout`; deadlock/serialization failures → 409 `concurrent_update`; SQLSTATE 08/53/57/58 and a closed pool → 503 `db_unavailable`. All other errors (including domain-layer business errors returned from within a `RunInTx` callback) pass through unchanged.

## ENUMs

```sql
CREATE TYPE entitlement_state AS ENUM (
    'active', 'trial', 'grace_period', 'suspended', 'offboarded', 'provisioning'
);

CREATE TYPE effective_set_cause AS ENUM (
    'plan_change', 'override_change', 'addon_change', 'trial_change',
    'catalog_refresh', 'billing_event', 'manual_recompute',
    'tenant_lifecycle', 'shortfall_resolution'
);

CREATE TYPE quota_addon_status AS ENUM (
    'active', 'expired', 'consumed', 'revoked'
);

CREATE TYPE override_action AS ENUM (
    'set', 'removed', 'expired'
);

CREATE TYPE shortfall_status AS ENUM (
    'pending', 'auto_accepted', 'pending_approval', 'approved', 'rejected', 'expired'
);

CREATE TYPE shortfall_disposition AS ENUM (
    'approve', 'reject'
);

CREATE TYPE trial_profile AS ENUM (
    'trial_specific', 'reduced_plan', 'full_plan', 'custom'
);
```

## Tables

| Table | Key columns / purpose |
|-------|----------------------|
| `entitlement_snapshots` | PK `tenant_id` (one row per tenant). `version BIGINT` (monotonically increasing; upsert only applies when incoming > stored — ENT-INV-1). `plan TEXT`, `state entitlement_state`, `limits JSONB`, `feature_flags JSONB`, `model_tier TEXT`, `trial_profile TEXT`. `cause effective_set_cause`. `computed_at TIMESTAMPTZ`. |
| `entitlement_overrides` | PK `id UUID`. `tenant_id`, `limit_key TEXT`, `value BIGINT`, `expires_at TIMESTAMPTZ` (nullable — no expiry = permanent). `set_by TEXT`, `reason TEXT`. `record_version BIGINT`. Partial unique index `uq_overrides_active (tenant_id, limit_key) WHERE deleted_at IS NULL`. |
| `override_history` | Append-only audit of every override lifecycle event. `tenant_id`, `limit_key`, `action override_action`, `old_value`/`new_value`, `actor TEXT`, `occurred_at`. No RLS (audit-only). |
| `quota_addons` | Quota addon packs. `id`, `tenant_id`, `limit_key TEXT`, `total_tokens BIGINT`, `remaining_tokens BIGINT`, `status quota_addon_status`. `expires_at TIMESTAMPTZ` (nullable). `granted_by TEXT`, `reason TEXT`. `record_version`. Optimistic locking on `remaining_tokens` decrements. |
| `quota_addon_drawdowns` | Per-request drawdown records. `id`, `addon_id`, `tenant_id`, `limit_key`, `tokens_consumed BIGINT`, `period_start TEXT (YYYY-MM)`, `request_id UUID` (idempotency key), `recorded_at`. Unique on `(tenant_id, request_id)` (ENT-INV-3). |
| `addon_shortfalls` | Shortfall lifecycle. `id`, `tenant_id`, `limit_key`, `requested BIGINT`, `available BIGINT`, `deficit BIGINT`. `status shortfall_status`. `auto_threshold TEXT`, `disposition shortfall_disposition`. `created_at`, `updated_at`. `record_version`. |
| `tenant_trial_assignments` | PK `tenant_id`. One trial assignment per tenant at a time. `plan_code TEXT`, `profile trial_profile`, `starts_at`, `ends_at`, `set_by`. `record_version`. Upserted on conflict. |
| `trial_entitlement_overrides` | Trial-scoped per-key limit overrides. PK `(tenant_id, limit_key)`. `value BIGINT`, `set_by TEXT`. Only applied when `state = trial` (ENT-INV-5). |
| `plan_catalog_cache` | Local cache of plan definitions fetched from IAM catalog. PK `plan_code TEXT`. `display_name`, `definition JSONB`, `record_version`, `cached_at`. Invalidated by `PlanCatalogChanged` events and `catalog-refresh` job. |
| `processed_events` | Consumer-idempotency ledger. PK `(event_id UUID, consumer TEXT)`. 8-day retention. No RLS, no FK. Only written via `inbox.Store.Process`. |

**Library-owned tables (not counted above):** `outbox_events`/`outbox_dead_letters` (`outbox.ApplySchema`) and `pending_audit_entries` (`auditoutbox.ApplySchema`). Their schema is the library's; this service only adds grants.

## Row-Level Security

Every tenant-scoped table (`entitlement_snapshots`, `entitlement_overrides`, `quota_addons`, `quota_addon_drawdowns`, `addon_shortfalls`, `tenant_trial_assignments`, `trial_entitlement_overrides`) has:

```sql
ENABLE ROW LEVEL SECURITY;
FORCE ROW LEVEL SECURITY;
REVOKE ALL FROM PUBLIC;
CREATE POLICY tenant_isolation ON <table>
    USING (tenant_id = current_setting('app.tenant_id', true)::UUID);
GRANT SELECT, INSERT, UPDATE ON <table> TO commercial_entitlements_app;
```

**Exceptions:**
- `plan_catalog_cache` — global table, no RLS (plan codes are tenant-agnostic).
- `processed_events` — global idempotency ledger, no RLS.
- `override_history` — append-only audit, no RLS (read by operator tools only).

**RLS invariants:**
- **RLS-1** — Every tenant-scoped table runs `ENABLE + FORCE + policy + REVOKE ALL FROM PUBLIC`.
- **RLS-2** — Missing/malformed GUC → 0 rows returned, no writes (NULL never matches any UUID).
- **RLS-3** — Cross-tenant INSERT/UPDATE rejected by the policy's implicit `WITH CHECK`.
- **RLS-4** — Only `commercial_entitlements_migrator` holds BYPASSRLS. App role never bypasses RLS.
- **RLS-5** — Internal/consumer writes set `app.tenant_id` to the target tenant ID via `pgcommon.GUCSetFromContext` inside `RunInTx`.
- **RLS-6** — GUC is **transaction-local** (`SET LOCAL`). Auto-resets at COMMIT/ROLLBACK. Never leaks across a PgBouncer pooled backend.

## Triggers — `touch_row()`

```sql
CREATE OR REPLACE FUNCTION touch_row() RETURNS trigger AS $$
BEGIN
  NEW.updated_at     := now();
  NEW.record_version := OLD.record_version + 1;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
```

`BEFORE UPDATE ... FOR EACH ROW` on every `record_version`-carrying table.

**Trigger invariants (TRG-1..2):**
- **TRG-1** — Client code **never sets `record_version`** in an UPDATE's SET clause. The trigger owns it. Optimistic-lock `UPDATE ... WHERE id=$1 AND record_version=$2` reads the version in WHERE only.
- **TRG-2** — `updated_at` is DB-managed; client-supplied values are overwritten.

## Migrations

Current migrations in `internal/adapter/outbound/postgres/migrations/`:
- **`000001_initial_schema.{up,down}.sql`** — all tables, ENUMs, RLS, triggers, grants in one consolidated migration (service never deployed; no incremental history to preserve).

`postgres.MigrateSchema` runs `outbox.ApplySchema` then `auditoutbox.ApplySchema` **then** domain migrations (MIG-2). Runs once per release via the Helm pre-install/pre-upgrade hook Job (`cmd/migrate`); server runs at startup only when `RUN_MIGRATIONS` is unset or true.

Uses `MIGRATION_DATABASE_URL` (direct Postgres, **bypasses PgBouncer** — CONFIG-2; DDL incompatible with transaction pooling).

**Migration invariants (MIG-1..3):**
- **MIG-1** — A migration file on `origin/main` is **immutable**. Schema changes go in a new, additive `NNNNNN_<name>.{up,down}.sql`.
- **MIG-2** — `outbox.ApplySchema` and `auditoutbox.ApplySchema` run before domain migrations.
- **MIG-3** — Only `commercial_entitlements_migrator` holds BYPASSRLS.

**Migration rules for new tables:**
1. `CREATE TABLE IF NOT EXISTS` with PK `UUID DEFAULT gen_random_uuid()`.
2. `ENABLE ROW LEVEL SECURITY; FORCE ROW LEVEL SECURITY;` for tenant-scoped tables.
3. `CREATE POLICY tenant_isolation ON <table> USING (tenant_id = current_setting('app.tenant_id', true)::UUID);`
4. `REVOKE ALL ON <table> FROM PUBLIC;`
5. `GRANT SELECT, INSERT, UPDATE ON <table> TO commercial_entitlements_app;` (add DELETE if reconciler sweeps it).
6. `CREATE TRIGGER touch_<table> BEFORE UPDATE ON <table> FOR EACH ROW EXECUTE FUNCTION touch_row();` for tables with `record_version`.

## Concurrency Invariants (CONC-1..3)

- **CONC-1** — All 7 `record_version`-carrying tables use optimistic locking: `entitlement_overrides`, `quota_addons`, `addon_shortfalls`, `entitlement_snapshots`, `tenant_trial_assignments`, `trial_entitlement_overrides`, `plan_catalog_cache`. `processed_events` and `quota_addon_drawdowns` excluded.
- **CONC-2** — `UPDATE ... WHERE id=$1 AND record_version=$2`: `RowsAffected() == 0` → `domain.ErrOptimisticLockConflict`.
- **CONC-3** — 409 response includes current `record_version` for client retry.
