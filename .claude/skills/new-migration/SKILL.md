---
name: new-migration
description: Scaffold a new numbered Postgres migration file pair (up.sql + down.sql) following the RLS + touch_row() + GRANT conventions for commercial-entitlements. Pass the migration name as the argument (e.g. /new-migration add_plan_snapshots_index).
disable-model-invocation: true
---

## Usage

```
/new-migration <migration-name>
```

## What this skill does

1. Find the highest existing migration number in `internal/adapter/outbound/postgres/migrations/`.
2. Create `NNNNNN_<migration-name>.up.sql` and `NNNNNN_<migration-name>.down.sql`
   where NNNNNN is padded to 6 digits (e.g. `000002`).
3. Populate the files from the templates below, replacing `<table_name>` and
   `<migration-name>` with the values derived from the argument.

## Up template

```sql
-- <NNNNNN>_<migration-name>.up.sql
-- Commercial Entitlements — <human-readable description>
-- Runs after the previous migration; assumes outbox/audit schema already applied.

-- ── <Section> ─────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS <table_name> (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID         NOT NULL,
    -- TODO: add columns here
    record_version BIGINT     NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- RLS: tenant isolation via GUC app.tenant_id (set by pgcommon.GUCSetFromContext).
ALTER TABLE <table_name> ENABLE ROW LEVEL SECURITY;
ALTER TABLE <table_name> FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON <table_name>
    USING (tenant_id = current_setting('app.tenant_id', true)::UUID);

-- Auto-bump updated_at.
CREATE TRIGGER touch_<table_name>
    BEFORE UPDATE ON <table_name>
    FOR EACH ROW EXECUTE FUNCTION touch_row();

-- Revoke public access; grant minimal DML to app role.
REVOKE ALL ON <table_name> FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON <table_name> TO commercial_entitlements_app;
-- Add DELETE if the reconciler sweeps this table:
-- GRANT DELETE ON <table_name> TO commercial_entitlements_app;
```

## Down template

```sql
-- <NNNNNN>_<migration-name>.down.sql
DROP TABLE IF EXISTS <table_name>;
```

## Rules (MIG-1)

- Never edit a migration that already exists on `origin/main`. Add a new one.
- Migration numbers are global sequence — take the next number after the highest file in the directory.
- Every new table needs: RLS enabled + forced, `tenant_isolation` policy, `touch_row()` trigger, `REVOKE ALL FROM PUBLIC`, `GRANT` for exactly the DML the Go code uses.
- Use `TIMESTAMPTZ NOT NULL DEFAULT now()` for all timestamp columns.
- Use `gen_random_uuid()` as the primary key default (Postgres 13+).
