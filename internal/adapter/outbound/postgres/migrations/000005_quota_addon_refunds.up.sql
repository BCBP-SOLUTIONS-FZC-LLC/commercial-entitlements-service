-- F-3 / BL-OQ-20 W-5: business-key idempotency for Billing add-on refunds.
--
-- A refund was idempotent only on the envelope ID (processed_events), which
-- is pruned after ProcessedEventsTTLDays. A DLQ redrive, a visibility-timeout
-- cycle past the prune, or a re-publish under a new envelope ID applied the
-- refund twice. One row per Billing refund_id (billing_ref) is claimed in the
-- same transaction as the pack update; a second claim affects 0 rows
-- (ON CONFLICT DO NOTHING) and the refund is not applied again.

CREATE TABLE IF NOT EXISTS quota_addon_refunds (
    billing_ref    UUID        PRIMARY KEY,               -- Billing refund_id; one per refund, never reused
    tenant_id      UUID        NOT NULL,
    addon_id       UUID        NOT NULL REFERENCES quota_addons(id) ON DELETE RESTRICT,
    purchase_ref   UUID        NOT NULL,                  -- Billing purchase_id (= quota_addons.billing_ref)
    tokens_removed BIGINT      NOT NULL,                  -- actual reduction; 0 when nothing was unused
    applied_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT quota_addon_refunds_tokens_nonnegative CHECK (tokens_removed >= 0)
);

CREATE INDEX IF NOT EXISTS idx_quota_addon_refunds_addon
    ON quota_addon_refunds (addon_id);

ALTER TABLE quota_addon_refunds ENABLE ROW LEVEL SECURITY;
ALTER TABLE quota_addon_refunds FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_quota_addon_refunds ON quota_addon_refunds
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

REVOKE ALL ON quota_addon_refunds FROM PUBLIC;
-- Append-only ledger: no UPDATE or DELETE for the app role.
GRANT SELECT, INSERT ON quota_addon_refunds TO commercial_entitlements_app;
