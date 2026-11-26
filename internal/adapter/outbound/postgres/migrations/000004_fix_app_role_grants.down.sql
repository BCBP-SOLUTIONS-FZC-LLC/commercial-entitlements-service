-- Reverse migration 000004: revoke the corrected grants.
REVOKE SELECT, INSERT, UPDATE         ON entitlement_snapshots        FROM commercial_entitlements_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON entitlement_overrides        FROM commercial_entitlements_app;
REVOKE SELECT, INSERT                 ON entitlement_override_history  FROM commercial_entitlements_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON tenant_trial_assignments      FROM commercial_entitlements_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON trial_entitlement_overrides   FROM commercial_entitlements_app;
REVOKE SELECT, INSERT                 ON quota_addon_drawdowns         FROM commercial_entitlements_app;
REVOKE SELECT, INSERT, UPDATE         ON quota_addon_shortfalls        FROM commercial_entitlements_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON plan_catalog_cache            FROM commercial_entitlements_app;
REVOKE SELECT, INSERT                 ON processed_events              FROM commercial_entitlements_app;
REVOKE USAGE, SELECT ON SEQUENCE entitlement_override_history_id_seq   FROM commercial_entitlements_app;
REVOKE SELECT, INSERT, UPDATE         ON outbox_events                 FROM commercial_entitlements_app;
REVOKE SELECT, INSERT                 ON pending_audit_entries         FROM commercial_entitlements_app;
