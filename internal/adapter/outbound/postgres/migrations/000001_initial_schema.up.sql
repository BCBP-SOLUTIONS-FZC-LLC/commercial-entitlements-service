-- 000001_initial_schema.up.sql
-- Commercial Entitlements service – full initial schema.
-- Runs AFTER outbox.ApplySchema and auditoutbox.ApplySchema.
-- RLS: every table (except infrastructure tables) requires the GUC
--   app.tenant_id to be set so queries are always scoped to one tenant
--   (tenant-scoped app pool). The BYPASSRLS system pool used by the
--   reconciler bypasses this.

-- ── Utility trigger ────────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION touch_row()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

-- ── ENUMs ──────────────────────────────────────────────────────────────────

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

-- ── entitlement_snapshots ──────────────────────────────────────────────────
-- One row per tenant. The version column guards against out-of-order writes;
-- the UPSERT only applies when the incoming version is strictly greater.

CREATE TABLE IF NOT EXISTS entitlement_snapshots (
    tenant_id             UUID        PRIMARY KEY,
    version               BIGINT      NOT NULL DEFAULT 1,
    source_record_version BIGINT      NOT NULL DEFAULT 0,
    plan                  TEXT        NOT NULL,
    state                 entitlement_state NOT NULL DEFAULT 'active',
    feature_flags         JSONB       NOT NULL DEFAULT '[]',
    model_tier            TEXT        NOT NULL DEFAULT '',
    limits                JSONB       NOT NULL DEFAULT '{}',
    trial_profile         TEXT,
    cause                 effective_set_cause NOT NULL DEFAULT 'manual_recompute',
    computed_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT entitlement_snapshots_version_positive CHECK (version > 0)
);

CREATE INDEX IF NOT EXISTS idx_entitlement_snapshots_plan
    ON entitlement_snapshots (plan);

-- ── Row-level security on snapshots ───────────────────────────────────────

ALTER TABLE entitlement_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement_snapshots FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_snapshots ON entitlement_snapshots
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── entitlement_overrides ─────────────────────────────────────────────────
-- One row per tenant+limit_key; value NULL means unlimited.

CREATE TABLE IF NOT EXISTS entitlement_overrides (
    tenant_id      UUID        NOT NULL,
    limit_key      TEXT        NOT NULL,
    value          BIGINT,                -- NULL = unlimited
    reason         TEXT        NOT NULL,
    set_by         UUID        NOT NULL,
    expires_at     TIMESTAMPTZ,
    record_version BIGINT      NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, limit_key)
);

CREATE INDEX IF NOT EXISTS idx_entitlement_overrides_expires
    ON entitlement_overrides (expires_at)
    WHERE expires_at IS NOT NULL;

CREATE TRIGGER entitlement_overrides_touch
    BEFORE UPDATE ON entitlement_overrides
    FOR EACH ROW EXECUTE FUNCTION touch_row();

ALTER TABLE entitlement_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement_overrides FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_overrides ON entitlement_overrides
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── entitlement_override_history ──────────────────────────────────────────

CREATE TABLE IF NOT EXISTS entitlement_override_history (
    id         BIGSERIAL   PRIMARY KEY,
    tenant_id  UUID        NOT NULL,
    limit_key  TEXT        NOT NULL,
    old_value  BIGINT,
    new_value  BIGINT,
    action     override_action NOT NULL,
    reason     TEXT        NOT NULL DEFAULT '',
    actor_id   UUID        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_override_history_tenant_key
    ON entitlement_override_history (tenant_id, limit_key, created_at DESC);

ALTER TABLE entitlement_override_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement_override_history FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_override_history ON entitlement_override_history
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── quota_addons ──────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS quota_addons (
    id               UUID               PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID               NOT NULL,
    limit_key        TEXT               NOT NULL,
    total_tokens     BIGINT             NOT NULL,
    remaining_tokens BIGINT             NOT NULL,
    granted_by       UUID               NOT NULL,
    reason           TEXT               NOT NULL,
    status           quota_addon_status NOT NULL DEFAULT 'active',
    expires_at       TIMESTAMPTZ,
    record_version   BIGINT             NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ        NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ        NOT NULL DEFAULT now(),

    CONSTRAINT quota_addons_tokens_positive  CHECK (total_tokens > 0),
    CONSTRAINT quota_addons_remaining_valid  CHECK (remaining_tokens >= 0 AND remaining_tokens <= total_tokens)
);

CREATE INDEX IF NOT EXISTS idx_quota_addons_tenant_key_status
    ON quota_addons (tenant_id, limit_key, status)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_quota_addons_expires
    ON quota_addons (expires_at)
    WHERE expires_at IS NOT NULL AND status = 'active';

CREATE TRIGGER quota_addons_touch
    BEFORE UPDATE ON quota_addons
    FOR EACH ROW EXECUTE FUNCTION touch_row();

ALTER TABLE quota_addons ENABLE ROW LEVEL SECURITY;
ALTER TABLE quota_addons FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_quota_addons ON quota_addons
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── quota_addon_drawdowns ─────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS quota_addon_drawdowns (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    addon_id        UUID        NOT NULL REFERENCES quota_addons(id) ON DELETE RESTRICT,
    tenant_id       UUID        NOT NULL,
    limit_key       TEXT        NOT NULL,
    tokens_consumed BIGINT      NOT NULL,
    period_start    TIMESTAMPTZ NOT NULL,
    request_id      UUID,       -- idempotency key
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT quota_drawdowns_tokens_positive CHECK (tokens_consumed > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_quota_drawdowns_request
    ON quota_addon_drawdowns (tenant_id, request_id)
    WHERE request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_quota_drawdowns_tenant_key
    ON quota_addon_drawdowns (tenant_id, limit_key, recorded_at DESC);

ALTER TABLE quota_addon_drawdowns ENABLE ROW LEVEL SECURITY;
ALTER TABLE quota_addon_drawdowns FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_quota_drawdowns ON quota_addon_drawdowns
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── quota_addon_shortfalls ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS quota_addon_shortfalls (
    id               UUID                 PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID                 NOT NULL,
    drawdown_id      UUID                 NOT NULL,
    limit_key        TEXT                 NOT NULL,
    requested        BIGINT               NOT NULL,
    available        BIGINT               NOT NULL,
    shortfall        BIGINT               NOT NULL,
    status           shortfall_status     NOT NULL DEFAULT 'pending',
    disposition      shortfall_disposition,
    disposition_by   UUID,
    disposition_ref  TEXT,
    disposition_note TEXT,
    created_at       TIMESTAMPTZ          NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ          NOT NULL DEFAULT now(),
    resolved_at      TIMESTAMPTZ,

    CONSTRAINT shortfall_positive CHECK (shortfall > 0)
);

CREATE INDEX IF NOT EXISTS idx_shortfalls_tenant_status
    ON quota_addon_shortfalls (tenant_id, status, created_at DESC);

CREATE TRIGGER quota_addon_shortfalls_touch
    BEFORE UPDATE ON quota_addon_shortfalls
    FOR EACH ROW EXECUTE FUNCTION touch_row();

ALTER TABLE quota_addon_shortfalls ENABLE ROW LEVEL SECURITY;
ALTER TABLE quota_addon_shortfalls FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_shortfalls ON quota_addon_shortfalls
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── tenant_trial_assignments ──────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS tenant_trial_assignments (
    tenant_id      UUID         PRIMARY KEY,
    plan_code      TEXT         NOT NULL,
    profile        trial_profile NOT NULL,
    starts_at      TIMESTAMPTZ  NOT NULL,
    ends_at        TIMESTAMPTZ  NOT NULL,
    set_by         UUID         NOT NULL,
    record_version BIGINT       NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TRIGGER tenant_trial_assignments_touch
    BEFORE UPDATE ON tenant_trial_assignments
    FOR EACH ROW EXECUTE FUNCTION touch_row();

ALTER TABLE tenant_trial_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_trial_assignments FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_trial_assignments ON tenant_trial_assignments
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── trial_entitlement_overrides ───────────────────────────────────────────

CREATE TABLE IF NOT EXISTS trial_entitlement_overrides (
    tenant_id  UUID        NOT NULL,
    limit_key  TEXT        NOT NULL,
    value      BIGINT,     -- NULL = unlimited
    set_by     UUID        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, limit_key)
);

CREATE TRIGGER trial_entitlement_overrides_touch
    BEFORE UPDATE ON trial_entitlement_overrides
    FOR EACH ROW EXECUTE FUNCTION touch_row();

ALTER TABLE trial_entitlement_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE trial_entitlement_overrides FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_trial_overrides ON trial_entitlement_overrides
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- ── plan_catalog_cache ────────────────────────────────────────────────────
-- Postgres-side cache for plan definitions fetched from IAM catalog admin.
-- Advisory only: Valkey is the primary cache, this is a warm fallback.

CREATE TABLE IF NOT EXISTS plan_catalog_cache (
    plan_code      TEXT        PRIMARY KEY,
    display_name   TEXT        NOT NULL DEFAULT '',
    record_version BIGINT      NOT NULL DEFAULT 0,
    definition     JSONB       NOT NULL DEFAULT '{}',
    cached_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── processed_events (at-most-once inbox) ─────────────────────────────────
-- Tracks which SQS event IDs have already been handled per consumer.

CREATE TABLE IF NOT EXISTS processed_events (
    consumer      TEXT        NOT NULL,
    event_id      TEXT        NOT NULL,
    event_type    TEXT        NOT NULL DEFAULT '',
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (consumer, event_id)
);

CREATE INDEX IF NOT EXISTS idx_processed_events_cleanup
    ON processed_events (processed_at);
