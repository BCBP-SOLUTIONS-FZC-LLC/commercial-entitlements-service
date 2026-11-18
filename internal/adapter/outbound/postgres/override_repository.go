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

// OverrideRepository persists and retrieves operator-managed entitlement overrides.
type OverrideRepository struct {
	pool *pgcommon.Pool
}

// NewOverrideRepository returns a new OverrideRepository.
func NewOverrideRepository(pool *pgcommon.Pool) *OverrideRepository {
	return &OverrideRepository{pool: pool}
}

var _ port.OverrideRepository = (*OverrideRepository)(nil)

// Upsert inserts or updates an override for the given tenant+key.
func (r *OverrideRepository) Upsert(ctx context.Context, o *domain.Override) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO entitlement_overrides
				(tenant_id, limit_key, value, reason, set_by, expires_at, record_version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id, limit_key) DO UPDATE
			  SET value          = EXCLUDED.value,
			      reason         = EXCLUDED.reason,
			      set_by         = EXCLUDED.set_by,
			      expires_at     = EXCLUDED.expires_at,
			      record_version = EXCLUDED.record_version,
			      updated_at     = EXCLUDED.updated_at`,
			o.TenantID, string(o.LimitKey), o.Value,
			o.Reason, o.SetBy, o.ExpiresAt,
			o.RecordVersion, o.CreatedAt, o.UpdatedAt,
		)
		return err
	})
}

// Delete removes an override for a tenant+key.
func (r *OverrideRepository) Delete(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM entitlement_overrides WHERE tenant_id=$1 AND limit_key=$2`,
			tenantID, string(key))
		return err
	})
}

// Get returns the override for a tenant+key, or ErrOverrideNotFound.
func (r *OverrideRepository) Get(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) (*domain.Override, error) {
	var o domain.Override
	var lk string
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, limit_key, value, reason, set_by, expires_at, record_version, created_at, updated_at
			FROM entitlement_overrides
			WHERE tenant_id=$1 AND limit_key=$2`, tenantID, string(key)).
			Scan(&o.TenantID, &lk, &o.Value, &o.Reason, &o.SetBy, &o.ExpiresAt, &o.RecordVersion, &o.CreatedAt, &o.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOverrideNotFound
		}
		return nil, wrapConnErr(err)
	}
	o.LimitKey = domain.LimitKey(lk)
	return &o, nil
}

// ListByTenant returns all overrides for a tenant, ordered by limit_key.
func (r *OverrideRepository) ListByTenant(ctx context.Context, tenantID uuid.UUID) ([]domain.Override, error) {
	var out []domain.Override
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id, limit_key, value, reason, set_by, expires_at, record_version, created_at, updated_at
			FROM entitlement_overrides
			WHERE tenant_id=$1
			ORDER BY limit_key`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = collectOverrides(rows)
		return err
	})
	return out, err
}

// ListExpired returns all overrides whose expires_at is in the past.
func (r *OverrideRepository) ListExpired(ctx context.Context, before time.Time) ([]domain.Override, error) {
	var out []domain.Override
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id, limit_key, value, reason, set_by, expires_at, record_version, created_at, updated_at
			FROM entitlement_overrides
			WHERE expires_at IS NOT NULL AND expires_at <= $1`, before)
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = collectOverrides(rows)
		return err
	})
	return out, err
}

// InsertHistory records a mutation history entry.
func (r *OverrideRepository) InsertHistory(ctx context.Context, h *domain.OverrideHistory) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO entitlement_override_history
				(tenant_id, limit_key, old_value, new_value, action, reason, actor_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			h.TenantID, string(h.LimitKey), h.OldValue, h.NewValue,
			string(h.Action), h.Reason, h.ActorID, h.CreatedAt,
		)
		return err
	})
}

// ListHistory returns the audit trail for a tenant+key, newest first.
func (r *OverrideRepository) ListHistory(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) ([]domain.OverrideHistory, error) {
	var out []domain.OverrideHistory
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, limit_key, old_value, new_value, action, reason, actor_id, created_at
			FROM entitlement_override_history
			WHERE tenant_id=$1 AND limit_key=$2
			ORDER BY created_at DESC`, tenantID, string(key))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h domain.OverrideHistory
			var lk, action string
			if err := rows.Scan(&h.ID, &h.TenantID, &lk, &h.OldValue, &h.NewValue, &action, &h.Reason, &h.ActorID, &h.CreatedAt); err != nil {
				return err
			}
			h.LimitKey = domain.LimitKey(lk)
			h.Action = domain.OverrideAction(action)
			out = append(out, h)
		}
		return rows.Err()
	})
	return out, err
}

func collectOverrides(rows pgx.Rows) ([]domain.Override, error) {
	var out []domain.Override
	for rows.Next() {
		var o domain.Override
		var lk string
		if err := rows.Scan(&o.TenantID, &lk, &o.Value, &o.Reason, &o.SetBy, &o.ExpiresAt, &o.RecordVersion, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.LimitKey = domain.LimitKey(lk)
		out = append(out, o)
	}
	return out, rows.Err()
}
