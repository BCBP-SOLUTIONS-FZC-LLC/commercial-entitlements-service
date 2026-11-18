package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// TrialRepository persists trial assignments and trial-scoped overrides for a
// tenant using the withPool pattern so writes join the outer TxRunner transaction.
type TrialRepository struct {
	pool *pgcommon.Pool
}

// NewTrialRepository returns a new TrialRepository.
func NewTrialRepository(pool *pgcommon.Pool) *TrialRepository {
	return &TrialRepository{pool: pool}
}

var _ port.TrialRepository = (*TrialRepository)(nil)

// UpsertAssignment creates or replaces a trial assignment.
func (r *TrialRepository) UpsertAssignment(ctx context.Context, a *domain.TenantTrialAssignment) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenant_trial_assignments
				(tenant_id, plan_code, profile, starts_at, ends_at,
				 set_by, record_version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id) DO UPDATE SET
				plan_code      = EXCLUDED.plan_code,
				profile        = EXCLUDED.profile,
				starts_at      = EXCLUDED.starts_at,
				ends_at        = EXCLUDED.ends_at,
				set_by         = EXCLUDED.set_by,
				record_version = EXCLUDED.record_version,
				updated_at     = EXCLUDED.updated_at`,
			a.TenantID, a.PlanCode, string(a.Profile),
			a.StartsAt, a.EndsAt, a.SetBy,
			a.RecordVersion, a.CreatedAt, a.UpdatedAt,
		)
		return err
	})
}

// GetAssignment retrieves the trial assignment for a tenant.
func (r *TrialRepository) GetAssignment(ctx context.Context, tenantID uuid.UUID) (*domain.TenantTrialAssignment, error) {
	var a domain.TenantTrialAssignment
	var profile string
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, plan_code, profile, starts_at, ends_at,
			       set_by, record_version, created_at, updated_at
			FROM tenant_trial_assignments WHERE tenant_id=$1`, tenantID).
			Scan(&a.TenantID, &a.PlanCode, &profile,
				&a.StartsAt, &a.EndsAt, &a.SetBy,
				&a.RecordVersion, &a.CreatedAt, &a.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTrialAssignmentNotFound
		}
		return nil, wrapConnErr(err)
	}
	a.Profile = domain.TrialProfile(profile)
	return &a, nil
}

// DeleteAssignment removes the trial assignment for a tenant.
func (r *TrialRepository) DeleteAssignment(ctx context.Context, tenantID uuid.UUID) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM tenant_trial_assignments WHERE tenant_id=$1`, tenantID)
		return err
	})
}

// UpsertOverride creates or replaces a trial-scoped override.
func (r *TrialRepository) UpsertOverride(ctx context.Context, o *domain.TrialEntitlementOverride) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO trial_entitlement_overrides
				(tenant_id, limit_key, value, set_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (tenant_id, limit_key) DO UPDATE SET
				value      = EXCLUDED.value,
				set_by     = EXCLUDED.set_by,
				updated_at = EXCLUDED.updated_at`,
			o.TenantID, string(o.LimitKey), o.Value, o.SetBy, o.CreatedAt, o.UpdatedAt,
		)
		return err
	})
}

// GetOverride retrieves a single trial override.
func (r *TrialRepository) GetOverride(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) (*domain.TrialEntitlementOverride, error) {
	var o domain.TrialEntitlementOverride
	var lk string
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, limit_key, value, set_by, created_at, updated_at
			FROM trial_entitlement_overrides
			WHERE tenant_id=$1 AND limit_key=$2`, tenantID, string(limitKey)).
			Scan(&o.TenantID, &lk, &o.Value, &o.SetBy, &o.CreatedAt, &o.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTrialOverrideNotFound
		}
		return nil, wrapConnErr(err)
	}
	o.LimitKey = domain.LimitKey(lk)
	return &o, nil
}

// ListOverrides returns all trial overrides for a tenant.
func (r *TrialRepository) ListOverrides(ctx context.Context, tenantID uuid.UUID) ([]domain.TrialEntitlementOverride, error) {
	var out []domain.TrialEntitlementOverride
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id, limit_key, value, set_by, created_at, updated_at
			FROM trial_entitlement_overrides WHERE tenant_id=$1
			ORDER BY limit_key`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o domain.TrialEntitlementOverride
			var lk string
			if err := rows.Scan(&o.TenantID, &lk, &o.Value, &o.SetBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
				return err
			}
			o.LimitKey = domain.LimitKey(lk)
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

// DeleteOverride removes a single trial override.
func (r *TrialRepository) DeleteOverride(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM trial_entitlement_overrides WHERE tenant_id=$1 AND limit_key=$2`,
			tenantID, string(limitKey))
		return err
	})
}

// DeleteAllOverrides removes all trial overrides for a tenant.
func (r *TrialRepository) DeleteAllOverrides(ctx context.Context, tenantID uuid.UUID) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM trial_entitlement_overrides WHERE tenant_id=$1`, tenantID)
		return err
	})
}
