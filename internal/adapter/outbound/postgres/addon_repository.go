package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// AddonRepository persists quota addon packs using the withPool pattern.
type AddonRepository struct {
	pool *pgcommon.Pool
}

// NewAddonRepository returns a new AddonRepository.
func NewAddonRepository(pool *pgcommon.Pool) *AddonRepository {
	return &AddonRepository{pool: pool}
}

var _ port.AddonRepository = (*AddonRepository)(nil)

// Insert creates a new quota addon pack.
func (r *AddonRepository) Insert(ctx context.Context, a *domain.QuotaAddon) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO quota_addons
				(id, tenant_id, limit_key, total_tokens, remaining_tokens, granted_by,
				 reason, status, expires_at, record_version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			a.ID, a.TenantID, string(a.LimitKey), a.TotalTokens, a.RemainingTokens,
			a.GrantedBy, a.Reason, string(a.Status), a.ExpiresAt,
			a.RecordVersion, a.CreatedAt, a.UpdatedAt,
		)
		return err
	})
}

// Update applies mutations to an existing addon pack.
func (r *AddonRepository) Update(ctx context.Context, a *domain.QuotaAddon) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE quota_addons
			SET remaining_tokens = $3, status = $4,
			    record_version   = $5, updated_at = $6
			WHERE id=$1 AND tenant_id=$2`,
			a.ID, a.TenantID,
			a.RemainingTokens, string(a.Status),
			a.RecordVersion, a.UpdatedAt,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrOptimisticLockConflict
		}
		return nil
	})
}

// GetByID fetches a single addon by ID regardless of tenant (operator reads).
func (r *AddonRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.QuotaAddon, error) {
	var a domain.QuotaAddon
	var lk, status string
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, tenant_id, limit_key, total_tokens, remaining_tokens, granted_by,
			       reason, status, expires_at, record_version, created_at, updated_at
			FROM quota_addons WHERE id=$1`, id).
			Scan(&a.ID, &a.TenantID, &lk, &a.TotalTokens, &a.RemainingTokens,
				&a.GrantedBy, &a.Reason, &status, &a.ExpiresAt,
				&a.RecordVersion, &a.CreatedAt, &a.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAddonNotFound
		}
		return nil, wrapConnErr(err)
	}
	a.LimitKey = domain.LimitKey(lk)
	a.Status = domain.QuotaAddonStatus(status)
	return &a, nil
}

// ListByTenant returns all addon packs for a tenant, optionally filtered by key.
func (r *AddonRepository) ListByTenant(ctx context.Context, tenantID uuid.UUID, limitKey *domain.LimitKey) ([]domain.QuotaAddon, error) {
	var out []domain.QuotaAddon
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		var rows pgx.Rows
		var err error
		if limitKey != nil {
			rows, err = tx.Query(ctx, `
				SELECT id, tenant_id, limit_key, total_tokens, remaining_tokens, granted_by,
				       reason, status, expires_at, record_version, created_at, updated_at
				FROM quota_addons WHERE tenant_id=$1 AND limit_key=$2
				ORDER BY created_at DESC`, tenantID, string(*limitKey))
		} else {
			rows, err = tx.Query(ctx, `
				SELECT id, tenant_id, limit_key, total_tokens, remaining_tokens, granted_by,
				       reason, status, expires_at, record_version, created_at, updated_at
				FROM quota_addons WHERE tenant_id=$1
				ORDER BY created_at DESC`, tenantID)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = collectAddons(rows)
		return err
	})
	return out, err
}

// ListExpired returns active addons whose expires_at is on or before `before`.
func (r *AddonRepository) ListExpired(ctx context.Context, before time.Time) ([]domain.QuotaAddon, error) {
	var out []domain.QuotaAddon
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, limit_key, total_tokens, remaining_tokens, granted_by,
			       reason, status, expires_at, record_version, created_at, updated_at
			FROM quota_addons
			WHERE status='active' AND expires_at IS NOT NULL AND expires_at <= $1`, before)
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = collectAddons(rows)
		return err
	})
	return out, err
}

// ListActiveByTenantAndKey returns active addons for a tenant+key, ordered by
// soonest-expiring first (FIFO drawdown order).
func (r *AddonRepository) ListActiveByTenantAndKey(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddon, error) {
	var out []domain.QuotaAddon
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, limit_key, total_tokens, remaining_tokens, granted_by,
			       reason, status, expires_at, record_version, created_at, updated_at
			FROM quota_addons
			WHERE tenant_id=$1 AND limit_key=$2 AND status='active' AND remaining_tokens > 0
			ORDER BY expires_at ASC NULLS LAST, created_at ASC`,
			tenantID, string(limitKey))
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = collectAddons(rows)
		return err
	})
	return out, err
}

// SumRemainingByTenantAndKey returns the total remaining tokens across all
// active addons for a tenant+key.
func (r *AddonRepository) SumRemainingByTenantAndKey(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) (int64, error) {
	var total int64
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(remaining_tokens),0)
			FROM quota_addons
			WHERE tenant_id=$1 AND limit_key=$2 AND status='active'`,
			tenantID, string(limitKey)).Scan(&total)
	})
	return total, err
}

func collectAddons(rows pgx.Rows) ([]domain.QuotaAddon, error) {
	var out []domain.QuotaAddon
	for rows.Next() {
		var a domain.QuotaAddon
		var lk, status string
		if err := rows.Scan(
			&a.ID, &a.TenantID, &lk, &a.TotalTokens, &a.RemainingTokens,
			&a.GrantedBy, &a.Reason, &status, &a.ExpiresAt,
			&a.RecordVersion, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}
		a.LimitKey = domain.LimitKey(lk)
		a.Status = domain.QuotaAddonStatus(status)
		out = append(out, a)
	}
	return out, rows.Err()
}

// DrawdownRepository persists quota addon drawdown records.
type DrawdownRepository struct {
	pool *pgcommon.Pool
}

// NewDrawdownRepository returns a new DrawdownRepository.
func NewDrawdownRepository(pool *pgcommon.Pool) *DrawdownRepository {
	return &DrawdownRepository{pool: pool}
}

var _ port.DrawdownRepository = (*DrawdownRepository)(nil)

// Insert creates a new drawdown record.
func (r *DrawdownRepository) Insert(ctx context.Context, d *domain.QuotaAddonDrawdown) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO quota_addon_drawdowns
				(id, addon_id, tenant_id, limit_key, tokens_consumed, period_start, request_id, recorded_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			d.ID, d.AddonID, d.TenantID, string(d.LimitKey),
			d.TokensConsumed, d.PeriodStart, d.RequestID, d.RecordedAt,
		)
		return err
	})
}

// GetByRequestID looks up a drawdown by its idempotency request ID.
func (r *DrawdownRepository) GetByRequestID(ctx context.Context, tenantID uuid.UUID, requestID uuid.UUID) (*domain.QuotaAddonDrawdown, error) {
	var d domain.QuotaAddonDrawdown
	var lk string
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, addon_id, tenant_id, limit_key, tokens_consumed, period_start, request_id, recorded_at
			FROM quota_addon_drawdowns
			WHERE tenant_id=$1 AND request_id=$2`, tenantID, requestID).
			Scan(&d.ID, &d.AddonID, &d.TenantID, &lk,
				&d.TokensConsumed, &d.PeriodStart, &d.RequestID, &d.RecordedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, wrapConnErr(err)
	}
	d.LimitKey = domain.LimitKey(lk)
	return &d, nil
}

// ListByTenantAndKey returns drawdowns for a tenant+key, newest first.
func (r *DrawdownRepository) ListByTenantAndKey(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddonDrawdown, error) {
	var out []domain.QuotaAddonDrawdown
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, addon_id, tenant_id, limit_key, tokens_consumed, period_start, request_id, recorded_at
			FROM quota_addon_drawdowns
			WHERE tenant_id=$1 AND limit_key=$2
			ORDER BY recorded_at DESC`, tenantID, string(limitKey))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d domain.QuotaAddonDrawdown
			var lk string
			if err := rows.Scan(&d.ID, &d.AddonID, &d.TenantID, &lk,
				&d.TokensConsumed, &d.PeriodStart, &d.RequestID, &d.RecordedAt); err != nil {
				return err
			}
			d.LimitKey = domain.LimitKey(lk)
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}
