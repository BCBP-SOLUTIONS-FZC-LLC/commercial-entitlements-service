package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// SnapshotRepository persists and retrieves entitlement snapshots (one row per
// tenant). Upsert is guarded by a version column so stale recomputes cannot
// overwrite a newer snapshot (ENT-INV-1).
type SnapshotRepository struct {
	pool *pgcommon.Pool
}

// NewSnapshotRepository returns a new SnapshotRepository.
func NewSnapshotRepository(pool *pgcommon.Pool) *SnapshotRepository {
	return &SnapshotRepository{pool: pool}
}

var _ port.SnapshotRepository = (*SnapshotRepository)(nil)

// Upsert inserts or replaces the snapshot for the tenant, only when the
// incoming version is strictly greater than the stored one.
func (r *SnapshotRepository) Upsert(ctx context.Context, snap *domain.EntitlementSnapshot) error {
	limitsJSON, err := json.Marshal(snap.Limits)
	if err != nil {
		return fmt.Errorf("marshal limits: %w", err)
	}
	flagsJSON, err := json.Marshal(snap.FeatureFlags)
	if err != nil {
		return fmt.Errorf("marshal feature_flags: %w", err)
	}

	return withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO entitlement_snapshots (
				tenant_id, version, source_record_version, plan, state,
				feature_flags, model_tier, limits, trial_profile, cause, computed_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (tenant_id) DO UPDATE
			  SET version               = EXCLUDED.version,
			      source_record_version = EXCLUDED.source_record_version,
			      plan                  = EXCLUDED.plan,
			      state                 = EXCLUDED.state,
			      feature_flags         = EXCLUDED.feature_flags,
			      model_tier            = EXCLUDED.model_tier,
			      limits                = EXCLUDED.limits,
			      trial_profile         = EXCLUDED.trial_profile,
			      cause                 = EXCLUDED.cause,
			      computed_at           = EXCLUDED.computed_at
			WHERE entitlement_snapshots.version < EXCLUDED.version`,
			snap.TenantID,
			snap.Version,
			snap.SourceRecordVersion,
			snap.Plan,
			string(snap.State),
			flagsJSON,
			snap.ModelTier,
			limitsJSON,
			snap.TrialProfile,
			string(snap.Cause),
			snap.ComputedAt,
		)
		return err
	})
}

// GetLatest returns the current snapshot for a tenant.
func (r *SnapshotRepository) GetLatest(ctx context.Context, tenantID uuid.UUID) (*domain.EntitlementSnapshot, error) {
	var snap domain.EntitlementSnapshot
	var limitsRaw, flagsRaw []byte
	var profile *string
	var state, cause string
	var computedAt time.Time

	err := withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, version, source_record_version, plan, state,
			       feature_flags, model_tier, limits, trial_profile, cause, computed_at
			FROM entitlement_snapshots
			WHERE tenant_id = $1`, tenantID).
			Scan(
				&snap.TenantID,
				&snap.Version,
				&snap.SourceRecordVersion,
				&snap.Plan,
				&state,
				&flagsRaw,
				&snap.ModelTier,
				&limitsRaw,
				&profile,
				&cause,
				&computedAt,
			)
	})
	if err != nil {
		if errors.Is(err, pgcommon.ErrNoRows) {
			return nil, domain.ErrSnapshotNotFound
		}
		return nil, wrapConnErr(err)
	}

	snap.State = domain.EntitlementState(state)
	snap.Cause = domain.EffectiveSetCause(cause)
	snap.TrialProfile = profile
	snap.ComputedAt = computedAt

	if err := json.Unmarshal(limitsRaw, &snap.Limits); err != nil {
		return nil, fmt.Errorf("unmarshal limits: %w", err)
	}
	if err := json.Unmarshal(flagsRaw, &snap.FeatureFlags); err != nil {
		return nil, fmt.Errorf("unmarshal feature_flags: %w", err)
	}
	return &snap, nil
}

// ListTenantIDsByPlan returns all tenant IDs currently on the given plan.
// Used by the Temporal catalog fan-out workflow.
func (r *SnapshotRepository) ListTenantIDsByPlan(ctx context.Context, planCode string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id FROM entitlement_snapshots WHERE plan = $1`, planCode)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}
