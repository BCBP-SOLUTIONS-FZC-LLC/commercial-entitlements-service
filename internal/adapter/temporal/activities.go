package temporal

import (
	"context"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// Activities holds the dependencies needed by Temporal activity functions.
type Activities struct {
	effectSvc   port.EffectiveSetService
	overrideSvc port.OverrideService
	addonSvc    port.AddonService
	snapshots   port.SnapshotRepository
}

// NewActivities creates the Activities struct for Temporal worker registration.
func NewActivities(
	effectSvc port.EffectiveSetService,
	overrideSvc port.OverrideService,
	addonSvc port.AddonService,
	snapshots port.SnapshotRepository,
) *Activities {
	return &Activities{
		effectSvc:   effectSvc,
		overrideSvc: overrideSvc,
		addonSvc:    addonSvc,
		snapshots:   snapshots,
	}
}

// SnapshotRepositoryWithList is a narrowing interface for the fan-out activity.
type SnapshotRepositoryWithList interface {
	ListTenantIDsByPlan(ctx context.Context, planCode string) ([]uuid.UUID, error)
}

// ListTenantsByPlanActivity returns all tenant IDs that currently have the
// given plan code in their entitlement snapshot.
func (a *Activities) ListTenantsByPlanActivity(ctx context.Context, planCode string) ([]string, error) {
	activity.RecordHeartbeat(ctx, planCode)
	lister, ok := a.snapshots.(SnapshotRepositoryWithList)
	if !ok {
		return nil, nil
	}
	ids, err := lister.ListTenantIDsByPlan(ctx, planCode)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out, nil
}

// RecomputeEntitlementsActivity triggers a full recompute for a single tenant.
func (a *Activities) RecomputeEntitlementsActivity(ctx context.Context, tenantIDStr string, cause string) error {
	activity.RecordHeartbeat(ctx, tenantIDStr)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return err
	}
	_, err = a.effectSvc.Recompute(ctx, tenantID, domain.EffectiveSetCause(cause))
	return err
}

// ExpireOverridesActivity expires all overrides past their expiry date.
func (a *Activities) ExpireOverridesActivity(ctx context.Context) error {
	activity.RecordHeartbeat(ctx, "expire_overrides")
	_, err := a.overrideSvc.ExpireOverrides(ctx)
	return err
}

// ExpireAddonsActivity expires all addon packs past their expiry date.
func (a *Activities) ExpireAddonsActivity(ctx context.Context) error {
	activity.RecordHeartbeat(ctx, "expire_addons")
	_, err := a.addonSvc.ExpireAddons(ctx)
	return err
}
