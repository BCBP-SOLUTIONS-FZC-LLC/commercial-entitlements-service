-- ENT-D16: a pack whose whole purchased quantity was refunded by Billing moves
-- to status 'refunded' (zero effective balance). total_tokens stays the
-- original purchased quantity; refunded amounts live in quota_addon_refunds.
-- ADD VALUE is allowed in a transaction on PostgreSQL >= 12 as long as the
-- new value is not used in the same transaction (it is not, here).
ALTER TYPE quota_addon_status ADD VALUE IF NOT EXISTS 'refunded';
