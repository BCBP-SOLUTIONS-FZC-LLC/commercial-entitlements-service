-- E-1 / BL-OQ-4: add billing_ref to quota_addons for Billing purchase idempotency.
-- Nullable: only packs created from TenantQuotaAddOnChanged {reason: "purchase"} have this set.
-- UNIQUE: prevents duplicate grants even if the same billing_ref arrives with different event IDs.

ALTER TABLE quota_addons
    ADD COLUMN IF NOT EXISTS billing_ref TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_quota_addons_billing_ref
    ON quota_addons (billing_ref)
    WHERE billing_ref IS NOT NULL;

GRANT SELECT, INSERT, UPDATE ON quota_addons TO entitlements_app;
