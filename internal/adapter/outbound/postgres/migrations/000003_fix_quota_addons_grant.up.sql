-- Fix stale role name from migration 000002.
-- 000002 incorrectly granted to "entitlements_app" (a role that does not
-- exist); the correct app role is "commercial_entitlements_app".
-- REVOKE on a non-existent role is a no-op when the role never held the
-- privilege, so this is safe to run even if 000002 never succeeded.

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'entitlements_app') THEN
    REVOKE SELECT, INSERT, UPDATE ON quota_addons FROM entitlements_app;
  END IF;
END
$$;

GRANT SELECT, INSERT, UPDATE ON quota_addons TO commercial_entitlements_app;
