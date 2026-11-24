-- 000001_initial_schema.down.sql
-- Drops all tables and types in reverse dependency order.

DROP TABLE IF EXISTS processed_events;
DROP TABLE IF EXISTS plan_catalog_cache;
DROP TABLE IF EXISTS trial_entitlement_overrides;
DROP TABLE IF EXISTS tenant_trial_assignments;
DROP TABLE IF EXISTS quota_addon_shortfalls;
DROP TABLE IF EXISTS quota_addon_drawdowns;
DROP TABLE IF EXISTS quota_addons;
DROP TABLE IF EXISTS entitlement_override_history;
DROP TABLE IF EXISTS entitlement_overrides;
DROP TABLE IF EXISTS entitlement_snapshots;

DROP TYPE IF EXISTS trial_profile;
DROP TYPE IF EXISTS shortfall_disposition;
DROP TYPE IF EXISTS shortfall_status;
DROP TYPE IF EXISTS override_action;
DROP TYPE IF EXISTS quota_addon_status;
DROP TYPE IF EXISTS effective_set_cause;
DROP TYPE IF EXISTS entitlement_state;

DROP FUNCTION IF EXISTS touch_row();
