-- E-9 (HLD v0.69 P-5a, CD-55): at tenant purge every active pack is forfeited.
-- Rows are kept, not deleted: quota_addon_drawdowns and quota_addon_refunds
-- reference quota_addons ON DELETE RESTRICT and are 7-year financial records.
-- ADD VALUE is allowed in a transaction on PostgreSQL >= 12 as long as the
-- new value is not used in the same transaction (it is not, here).
ALTER TYPE quota_addon_status ADD VALUE IF NOT EXISTS 'forfeited';
