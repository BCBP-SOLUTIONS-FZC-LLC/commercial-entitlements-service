-- Fix stale role name throughout all grants.
-- Migrations 000001 and 000002 incorrectly granted to "entitlements_app"
-- (a role that does not exist); the correct app role for this service is
-- "commercial_entitlements_app".  Migration 000003 already fixed the
-- quota_addons grant; this migration covers every remaining table, the
-- sequence, and the two platform-library-managed tables (outbox_events,
-- pending_audit_entries) whose grants were never added.

-- Domain tables — mirrors the intent of 000001's GRANT block with the correct role.
GRANT SELECT, INSERT, UPDATE         ON entitlement_snapshots             TO commercial_entitlements_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON entitlement_overrides             TO commercial_entitlements_app;
GRANT SELECT, INSERT                 ON entitlement_override_history      TO commercial_entitlements_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_trial_assignments          TO commercial_entitlements_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON trial_entitlement_overrides       TO commercial_entitlements_app;
GRANT SELECT, INSERT                 ON quota_addon_drawdowns             TO commercial_entitlements_app;
GRANT SELECT, INSERT, UPDATE         ON quota_addon_shortfalls            TO commercial_entitlements_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON plan_catalog_cache                TO commercial_entitlements_app;
GRANT SELECT, INSERT                 ON processed_events                  TO commercial_entitlements_app;
GRANT USAGE, SELECT ON SEQUENCE entitlement_override_history_id_seq       TO commercial_entitlements_app;

-- Platform-library-managed tables — created by platform-events and
-- platform-audit's internal migrations; not in 000001 at all.
GRANT SELECT, INSERT, UPDATE         ON outbox_events                     TO commercial_entitlements_app;
GRANT SELECT, INSERT                 ON pending_audit_entries             TO commercial_entitlements_app;

-- Clean up the phantom role grant if it somehow exists.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'entitlements_app') THEN
    REVOKE ALL ON ALL TABLES IN SCHEMA public FROM entitlements_app;
    REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM entitlements_app;
  END IF;
END
$$;
