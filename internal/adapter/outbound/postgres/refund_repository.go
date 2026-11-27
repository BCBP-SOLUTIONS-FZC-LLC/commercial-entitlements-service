package postgres

import (
	"context"
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// RefundRepository persists the quota_addon_refunds ledger (F-3).
type RefundRepository struct {
	pool *pgcommon.Pool
}

// NewRefundRepository returns a new RefundRepository.
func NewRefundRepository(pool *pgcommon.Pool) *RefundRepository {
	return &RefundRepository{pool: pool}
}

var _ port.RefundRepository = (*RefundRepository)(nil)

// Claim inserts the refund row. ON CONFLICT DO NOTHING keeps the caller's
// transaction (the consumer's inbox tx) usable when billing_ref is already
// recorded; a raised unique violation would abort it (see B-8).
func (r *RefundRepository) Claim(ctx context.Context, ref *domain.QuotaAddonRefund) (bool, error) {
	var inserted bool
	err := withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO quota_addon_refunds
				(billing_ref, tenant_id, addon_id, purchase_ref, tokens_removed, applied_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (billing_ref) DO NOTHING`,
			ref.BillingRef, ref.TenantID, ref.AddonID, ref.PurchaseRef, ref.TokensRemoved, ref.AppliedAt,
		)
		if err != nil {
			return err
		}
		inserted = tag.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return false, wrapConnErr(err)
	}
	return inserted, nil
}

// GetForTenant returns the refund recorded for (tenantID, billingRef), or nil
// when this tenant has none. Under RLS another tenant's row is invisible, so
// nil after a failed Claim means the billing_ref belongs to a different tenant.
func (r *RefundRepository) GetForTenant(ctx context.Context, tenantID, billingRef uuid.UUID) (*domain.QuotaAddonRefund, error) {
	var ref domain.QuotaAddonRefund
	var found bool
	err := withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT billing_ref, tenant_id, addon_id, purchase_ref, tokens_removed, applied_at
			FROM quota_addon_refunds WHERE tenant_id=$1 AND billing_ref=$2`,
			tenantID, billingRef).Scan(&ref.BillingRef, &ref.TenantID, &ref.AddonID, &ref.PurchaseRef, &ref.TokensRemoved, &ref.AppliedAt)
		if errors.Is(err, pgcommon.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return nil, wrapConnErr(err)
	}
	if !found {
		return nil, nil
	}
	return &ref, nil
}

// SumByAddon returns the refunded tokens per addon (ledger is the system of
// record for refunded amounts, ENT-D16).
func (r *RefundRepository) SumByAddon(ctx context.Context, tenantID uuid.UUID, addonIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	out := make(map[uuid.UUID]int64, len(addonIDs))
	if len(addonIDs) == 0 {
		return out, nil
	}
	err := withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT addon_id, sum(tokens_removed)::bigint
			FROM quota_addon_refunds
			WHERE tenant_id=$1 AND addon_id = ANY($2)
			GROUP BY addon_id`, tenantID, addonIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var sum int64
			if err := rows.Scan(&id, &sum); err != nil {
				return err
			}
			out[id] = sum
		}
		return rows.Err()
	})
	if err != nil {
		return nil, wrapConnErr(err)
	}
	return out, nil
}
