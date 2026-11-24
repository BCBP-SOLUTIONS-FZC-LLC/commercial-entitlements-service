DROP INDEX IF EXISTS uq_quota_addons_billing_ref;

ALTER TABLE quota_addons
    DROP COLUMN IF EXISTS billing_ref;
