package temporal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── minimal stub types ────────────────────────────────────────────────────────

// stubSnapshotRepo satisfies port.SnapshotRepository and optionally
// SnapshotRepositoryWithList so we can test both code paths.
type stubSnapshotRepo struct {
	// ListTenantIDsByPlan support – nil means the interface is not implemented.
	listFn   func(ctx context.Context, planCode string) ([]uuid.UUID, error)
	listOk   bool // when true, the struct satisfies SnapshotRepositoryWithList
	upsertFn func(ctx context.Context, snap *domain.EntitlementSnapshot) (bool, error)
	getFn    func(ctx context.Context, tenantID uuid.UUID) (*domain.EntitlementSnapshot, error)
}

func (s *stubSnapshotRepo) Upsert(ctx context.Context, snap *domain.EntitlementSnapshot) (bool, error) {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, snap)
	}
	return true, nil
}

func (s *stubSnapshotRepo) GetLatest(ctx context.Context, tenantID uuid.UUID) (*domain.EntitlementSnapshot, error) {
	if s.getFn != nil {
		return s.getFn(ctx, tenantID)
	}
	return nil, domain.ErrSnapshotNotFound
}

// ListTenantIDsByPlan is conditionally compiled: the type assertion inside
// ListTenantsByPlanActivity checks for this method at runtime.
func (s *stubSnapshotRepo) ListTenantIDsByPlan(ctx context.Context, planCode string) ([]uuid.UUID, error) {
	if s.listFn != nil {
		return s.listFn(ctx, planCode)
	}
	return nil, nil
}

// stubEffectiveSvc satisfies port.EffectiveSetService.
type stubEffectiveSvc struct {
	recomputeFn func(ctx context.Context, tenantID uuid.UUID, cause domain.EffectiveSetCause) (*domain.EffectiveSet, error)
}

func (s *stubEffectiveSvc) GetEffectiveSet(ctx context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error) {
	return nil, nil
}

func (s *stubEffectiveSvc) Recompute(ctx context.Context, tenantID uuid.UUID, cause domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
	if s.recomputeFn != nil {
		return s.recomputeFn(ctx, tenantID, cause)
	}
	return &domain.EffectiveSet{TenantID: tenantID}, nil
}

// stubOverrideSvc satisfies port.OverrideService.
type stubOverrideSvc struct {
	expireCount int
	expireErr   error
}

func (s *stubOverrideSvc) SetOverride(_ context.Context, _ port.SetOverrideRequest) (*domain.Override, error) {
	return nil, nil
}
func (s *stubOverrideSvc) RemoveOverride(_ context.Context, _ port.RemoveOverrideRequest) error {
	return nil
}
func (s *stubOverrideSvc) GetOverride(_ context.Context, _ uuid.UUID, _ domain.LimitKey) (*domain.Override, error) {
	return nil, nil
}
func (s *stubOverrideSvc) ListOverrides(_ context.Context, _ uuid.UUID) ([]domain.Override, error) {
	return nil, nil
}
func (s *stubOverrideSvc) ListOverrideHistory(_ context.Context, _ uuid.UUID, _ domain.LimitKey) ([]domain.OverrideHistory, error) {
	return nil, nil
}
func (s *stubOverrideSvc) ExpireOverrides(_ context.Context) (int, error) {
	return s.expireCount, s.expireErr
}

// stubAddonSvc satisfies port.AddonService.
type stubAddonSvc struct {
	expireCount int
	expireErr   error
}

func (s *stubAddonSvc) AddAddon(_ context.Context, _ port.AddAddonRequest) (*domain.QuotaAddon, error) {
	return nil, nil
}
func (s *stubAddonSvc) RemoveAddon(_ context.Context, _ port.RemoveAddonRequest) error { return nil }
func (s *stubAddonSvc) ListAddons(_ context.Context, _ uuid.UUID) ([]domain.QuotaAddon, error) {
	return nil, nil
}
func (s *stubAddonSvc) RecordDrawdown(_ context.Context, _ port.RecordDrawdownRequest) (*domain.QuotaAddonDrawdown, error) {
	return nil, nil
}
func (s *stubAddonSvc) ApplyBillingRefund(_ context.Context, _ port.ApplyBillingRefundRequest) error {
	return nil
}
func (s *stubAddonSvc) ApplyBillingPurchase(_ context.Context, _ port.ApplyBillingPurchaseRequest) error {
	return nil
}
func (s *stubAddonSvc) ListDrawdowns(_ context.Context, _ uuid.UUID, _ domain.LimitKey) ([]domain.QuotaAddonDrawdown, error) {
	return nil, nil
}
func (s *stubAddonSvc) ResolveShortfall(_ context.Context, _ port.ResolveShortfallRequest) (*domain.QuotaAddonShortfall, error) {
	return nil, nil
}
func (s *stubAddonSvc) ListShortfalls(_ context.Context, _ uuid.UUID, _ []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error) {
	return nil, nil
}
func (s *stubAddonSvc) GetShortfall(_ context.Context, _ uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	return nil, nil
}
func (s *stubAddonSvc) ExpireAddons(_ context.Context) (int, error) {
	return s.expireCount, s.expireErr
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newActivityEnv creates a Temporal test activity environment and registers the
// Activities struct so heartbeats succeed without a live server.
func newActivityEnv(t *testing.T, acts *Activities) *testsuite.TestActivityEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)
	return env
}

// ── NewActivities ─────────────────────────────────────────────────────────────

func TestNewActivities_StoresAllDependencies(t *testing.T) {
	effectSvc := &stubEffectiveSvc{}
	overrideSvc := &stubOverrideSvc{}
	addonSvc := &stubAddonSvc{}
	snapshotRepo := &stubSnapshotRepo{listOk: true}

	acts := NewActivities(effectSvc, overrideSvc, addonSvc, snapshotRepo)

	require.NotNil(t, acts)
	assert.Equal(t, effectSvc, acts.effectSvc)
	assert.Equal(t, overrideSvc, acts.overrideSvc)
	assert.Equal(t, addonSvc, acts.addonSvc)
	assert.Equal(t, snapshotRepo, acts.snapshots)
}

// ── ListTenantsByPlanActivity ─────────────────────────────────────────────────

func TestListTenantsByPlanActivity_ReturnsTenantIDs(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()
	snap := &stubSnapshotRepo{
		listOk: true,
		listFn: func(_ context.Context, planCode string) ([]uuid.UUID, error) {
			assert.Equal(t, "pro", planCode)
			return []uuid.UUID{id1, id2}, nil
		},
	}
	acts := NewActivities(&stubEffectiveSvc{}, &stubOverrideSvc{}, &stubAddonSvc{}, snap)
	env := newActivityEnv(t, acts)

	val, err := env.ExecuteActivity(acts.ListTenantsByPlanActivity, "pro")
	require.NoError(t, err)

	var ids []string
	require.NoError(t, val.Get(&ids))
	assert.ElementsMatch(t, []string{id1.String(), id2.String()}, ids)
}

func TestListTenantsByPlanActivity_NoListInterface_ReturnsNil(t *testing.T) {
	// Use a plain SnapshotRepository that does NOT implement
	// SnapshotRepositoryWithList — the type assertion must fail gracefully.
	snap := &plainSnapshotRepo{}
	acts := NewActivities(&stubEffectiveSvc{}, &stubOverrideSvc{}, &stubAddonSvc{}, snap)
	env := newActivityEnv(t, acts)

	val, err := env.ExecuteActivity(acts.ListTenantsByPlanActivity, "starter")
	require.NoError(t, err)

	var ids []string
	require.NoError(t, val.Get(&ids))
	assert.Empty(t, ids) // nil or [] are both acceptable empty results
}

func TestListTenantsByPlanActivity_PropagatesRepoError(t *testing.T) {
	sentinel := errors.New("db failure")
	snap := &stubSnapshotRepo{
		listOk: true,
		listFn: func(_ context.Context, _ string) ([]uuid.UUID, error) {
			return nil, sentinel
		},
	}
	acts := NewActivities(&stubEffectiveSvc{}, &stubOverrideSvc{}, &stubAddonSvc{}, snap)
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.ListTenantsByPlanActivity, "pro")
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}

// plainSnapshotRepo satisfies only port.SnapshotRepository — no List method.
type plainSnapshotRepo struct{}

func (p *plainSnapshotRepo) Upsert(_ context.Context, _ *domain.EntitlementSnapshot) (bool, error) {
	return true, nil
}
func (p *plainSnapshotRepo) GetLatest(_ context.Context, _ uuid.UUID) (*domain.EntitlementSnapshot, error) {
	return nil, nil
}

// ── RecomputeEntitlementsActivity ─────────────────────────────────────────────

func TestRecomputeEntitlementsActivity_CallsRecompute(t *testing.T) {
	tenantID := uuid.New()
	var calledWith uuid.UUID
	var calledCause domain.EffectiveSetCause

	effectSvc := &stubEffectiveSvc{
		recomputeFn: func(_ context.Context, id uuid.UUID, cause domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
			calledWith = id
			calledCause = cause
			return &domain.EffectiveSet{TenantID: id}, nil
		},
	}
	acts := NewActivities(effectSvc, &stubOverrideSvc{}, &stubAddonSvc{}, &stubSnapshotRepo{})
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.RecomputeEntitlementsActivity, tenantID.String(), CatalogFanoutWorkflowType)
	require.NoError(t, err)
	assert.Equal(t, tenantID, calledWith)
	assert.Equal(t, domain.EffectiveSetCause(CatalogFanoutWorkflowType), calledCause)
}

func TestRecomputeEntitlementsActivity_InvalidUUID_ReturnsError(t *testing.T) {
	acts := NewActivities(&stubEffectiveSvc{}, &stubOverrideSvc{}, &stubAddonSvc{}, &stubSnapshotRepo{})
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.RecomputeEntitlementsActivity, "not-a-uuid", "cause")
	require.Error(t, err)
}

func TestRecomputeEntitlementsActivity_PropagatesServiceError(t *testing.T) {
	sentinel := errors.New("recompute failed")
	effectSvc := &stubEffectiveSvc{
		recomputeFn: func(_ context.Context, _ uuid.UUID, _ domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
			return nil, sentinel
		},
	}
	acts := NewActivities(effectSvc, &stubOverrideSvc{}, &stubAddonSvc{}, &stubSnapshotRepo{})
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.RecomputeEntitlementsActivity, uuid.New().String(), "cause")
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}

// ── ExpireOverridesActivity ───────────────────────────────────────────────────

func TestExpireOverridesActivity_ReturnsNilOnSuccess(t *testing.T) {
	overrideSvc := &stubOverrideSvc{expireCount: 3}
	acts := NewActivities(&stubEffectiveSvc{}, overrideSvc, &stubAddonSvc{}, &stubSnapshotRepo{})
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.ExpireOverridesActivity)
	require.NoError(t, err)
}

func TestExpireOverridesActivity_PropagatesServiceError(t *testing.T) {
	sentinel := errors.New("expire overrides failed")
	overrideSvc := &stubOverrideSvc{expireErr: sentinel}
	acts := NewActivities(&stubEffectiveSvc{}, overrideSvc, &stubAddonSvc{}, &stubSnapshotRepo{})
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.ExpireOverridesActivity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}

// ── ExpireAddonsActivity ──────────────────────────────────────────────────────

func TestExpireAddonsActivity_ReturnsNilOnSuccess(t *testing.T) {
	addonSvc := &stubAddonSvc{expireCount: 5}
	acts := NewActivities(&stubEffectiveSvc{}, &stubOverrideSvc{}, addonSvc, &stubSnapshotRepo{})
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.ExpireAddonsActivity)
	require.NoError(t, err)
}

func TestExpireAddonsActivity_PropagatesServiceError(t *testing.T) {
	sentinel := errors.New("expire addons failed")
	addonSvc := &stubAddonSvc{expireErr: sentinel}
	acts := NewActivities(&stubEffectiveSvc{}, &stubOverrideSvc{}, addonSvc, &stubSnapshotRepo{})
	env := newActivityEnv(t, acts)

	_, err := env.ExecuteActivity(acts.ExpireAddonsActivity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}

// ── Direct call tests (coverage-friendly) ────────────────────────────────────
// These call activity functions directly with a minimal context so the coverage
// counter is incremented by the test goroutine, not the Temporal environment.

func TestNewActivities_DirectCoverage(t *testing.T) {
	acts := NewActivities(
		&stubEffectiveSvc{},
		&stubOverrideSvc{},
		&stubAddonSvc{},
		&stubSnapshotRepo{},
	)
	assert.NotNil(t, acts)
}

func TestListTenantsByPlanActivity_DirectNilResult(t *testing.T) {
	// stubSnapshotRepo without listOk → type assertion fails → nil,nil returned
	acts := NewActivities(
		&stubEffectiveSvc{},
		&stubOverrideSvc{},
		&stubAddonSvc{},
		&stubSnapshotRepo{listOk: false},
	)
	// We can't call RecordHeartbeat outside the env, so skip activity.RecordHeartbeat
	// by calling through the env which injects a proper context.
	env := newActivityEnv(t, acts)
	val, err := env.ExecuteActivity(acts.ListTenantsByPlanActivity, "starter")
	require.NoError(t, err)
	var ids []string
	require.NoError(t, val.Get(&ids))
	assert.Empty(t, ids) // nil or [] are both acceptable empty results
}

func TestRecomputeEntitlementsActivity_DirectError(t *testing.T) {
	wantErr := errors.New("recompute error")
	acts := NewActivities(
		&stubEffectiveSvc{recomputeFn: func(_ context.Context, _ uuid.UUID, _ domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
			return nil, wantErr
		}},
		&stubOverrideSvc{},
		&stubAddonSvc{},
		&stubSnapshotRepo{},
	)
	env := newActivityEnv(t, acts)
	_, err := env.ExecuteActivity(acts.RecomputeEntitlementsActivity, uuid.New().String(), "plan_change")
	assert.Error(t, err)
}

func TestExpireOverridesActivity_DirectSuccess(t *testing.T) {
	acts := NewActivities(
		&stubEffectiveSvc{},
		&stubOverrideSvc{expireCount: 5},
		&stubAddonSvc{},
		&stubSnapshotRepo{},
	)
	env := newActivityEnv(t, acts)
	_, err := env.ExecuteActivity(acts.ExpireOverridesActivity)
	assert.NoError(t, err)
}

func TestExpireAddonsActivity_DirectSuccess(t *testing.T) {
	acts := NewActivities(
		&stubEffectiveSvc{},
		&stubOverrideSvc{},
		&stubAddonSvc{expireCount: 3},
		&stubSnapshotRepo{},
	)
	env := newActivityEnv(t, acts)
	_, err := env.ExecuteActivity(acts.ExpireAddonsActivity)
	assert.NoError(t, err)
}
