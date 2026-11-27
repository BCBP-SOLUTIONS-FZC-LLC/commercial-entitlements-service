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
func (r *SnapshotRepository) Upsert(ctx context.Context, snap *domain.EntitlementSnapshot) (bool, error) {
	limitsJSON, err := json.Marshal(snap.Limits)
	if err != nil {
		return false, fmt.Errorf("marshal limits: %w", err)
	}
	flagsJSON, err := json.Marshal(snap.FeatureFlags)
	if err != nil {
		return false, fmt.Errorf("marshal feature_flags: %w", err)
	}

	var written bool
	err = withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		tag, execErr := tx.Exec(ctx, `
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
			WHERE entitlement_snapshots.version < EXCLUDED.version
			  -- Never replace a snapshot computed from a newer Core tenant row
			  -- (I-17 record_version) with one computed from an older read: two
			  -- concurrent recomputes are ordered by version at write time, not
			  -- by when they read I-17 (B-3 lost update).
			  AND entitlement_snapshots.source_record_version <= EXCLUDED.source_record_version`,
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
		if execErr != nil {
			return execErr
		}
		written = tag.RowsAffected() > 0
		return nil
	})
	return written, err
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

// listTenantIDsByPlanPageSize is the maximum rows returned per
// ListTenantIDsByPlan call. Keeps the activity memory footprint bounded.
// When a plan has more tenants the caller (Temporal workflow) must page by
// passing the last returned ID as afterTenantID on subsequent calls.
// TODO: change the signature to accept (afterTenantID uuid.UUID, limit int)
// once the Temporal activities are updated to loop via continue-as-new paging.
const listTenantIDsByPlanPageSize = 5000

// ListTenantIDsByPlan returns up to listTenantIDsByPlanPageSize tenant IDs
// currently on the given plan, ordered by tenant_id for deterministic paging.
// Used by the Temporal catalog fan-out workflow (ENT-D1).
func (r *SnapshotRepository) ListTenantIDsByPlan(ctx context.Context, planCode string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id
			FROM entitlement_snapshots
			WHERE plan = $1
			ORDER BY tenant_id
			LIMIT $2`, planCode, listTenantIDsByPlanPageSize)
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
