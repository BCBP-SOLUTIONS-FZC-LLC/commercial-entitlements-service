-- Reverse migration 000003: revoke the correct grant (restores pre-003 state).
REVOKE SELECT, INSERT, UPDATE ON quota_addons FROM commercial_entitlements_app;
