// Package consumer — shared test fakes for all consumer tests.
package consumer

import (
	"context"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── fakeInbox ─────────────────────────────────────────────────────────────

type fakeInbox struct {
	duplicate bool
	err       error
}

func (f *fakeInbox) ProcessOnce(ctx context.Context, _, _ string, fn func(context.Context) error) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.duplicate {
		return true, nil
	}
	return false, fn(ctx)
}

// ── fakeEffectiveSvc ──────────────────────────────────────────────────────

type fakeEffectiveSvc struct {
	recomputeErr   error
	recomputedWith []domain.EffectiveSetCause
}

func (f *fakeEffectiveSvc) GetEffectiveSet(_ context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error) {
	return &domain.EffectiveSet{TenantID: tenantID}, nil
}

func (f *fakeEffectiveSvc) Recompute(_ context.Context, tenantID uuid.UUID, cause domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
	f.recomputedWith = append(f.recomputedWith, cause)
	if f.recomputeErr != nil {
		return nil, f.recomputeErr
	}
	return &domain.EffectiveSet{TenantID: tenantID}, nil
}

// ── fakeAddonSvc ──────────────────────────────────────────────────────────

type fakeAddonSvc struct {
	applyPurchaseErr error
	applyRefundErr   error
	purchaseCalled   bool
	refundCalled     bool
	forfeited        []uuid.UUID
	forfeitErr       error
}

func (f *fakeAddonSvc) ApplyBillingPurchase(_ context.Context, _ port.ApplyBillingPurchaseRequest) error {
	f.purchaseCalled = true
	return f.applyPurchaseErr
}

func (f *fakeAddonSvc) ApplyBillingRefund(_ context.Context, _ port.ApplyBillingRefundRequest) error {
	f.refundCalled = true
	return f.applyRefundErr
}

func (f *fakeAddonSvc) AddAddon(_ context.Context, _ port.AddAddonRequest) (*domain.QuotaAddon, error) {
	return nil, nil
}
func (f *fakeAddonSvc) RemoveAddon(_ context.Context, _ port.RemoveAddonRequest) error { return nil }
func (f *fakeAddonSvc) ListAddons(_ context.Context, _ uuid.UUID) ([]domain.QuotaAddon, error) {
	return nil, nil
}
func (f *fakeAddonSvc) RecordDrawdown(_ context.Context, _ port.RecordDrawdownRequest) (*domain.QuotaAddonDrawdown, error) {
	return nil, nil
}
func (f *fakeAddonSvc) ListDrawdowns(_ context.Context, _ uuid.UUID, _ domain.LimitKey) ([]domain.QuotaAddonDrawdown, error) {
	return nil, nil
}
func (f *fakeAddonSvc) ResolveShortfall(_ context.Context, _ port.ResolveShortfallRequest) (*domain.QuotaAddonShortfall, error) {
	return nil, nil
}
func (f *fakeAddonSvc) ListShortfalls(_ context.Context, _ uuid.UUID, _ []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error) {
	return nil, nil
}
func (f *fakeAddonSvc) GetShortfall(_ context.Context, _ uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	return nil, nil
}
func (f *fakeAddonSvc) ExpireAddons(_ context.Context) (int, error) { return 0, nil }
func (f *fakeAddonSvc) ForfeitAddonsAtPurge(_ context.Context, tenantID uuid.UUID) (int, error) {
	f.forfeited = append(f.forfeited, tenantID)
	return 1, f.forfeitErr
}

// ── fakeCatalogSvc ────────────────────────────────────────────────────────

type fakeCatalogSvc struct {
	refreshErr   error
	fanoutErr    error
	refreshCalls []string
}

func (f *fakeCatalogSvc) GetPlan(_ context.Context, planCode string) (*domain.Plan, error) {
	return &domain.Plan{Code: planCode}, nil
}
func (f *fakeCatalogSvc) RefreshPlan(_ context.Context, planCode string) (*domain.Plan, error) {
	f.refreshCalls = append(f.refreshCalls, planCode)
	return &domain.Plan{Code: planCode}, f.refreshErr
}
func (f *fakeCatalogSvc) FanoutCatalogRefresh(_ context.Context, _ string) error {
	return f.fanoutErr
}
func (f *fakeCatalogSvc) ListPlans(_ context.Context) ([]string, error) { return nil, nil }

// ── fakeCache implements port.Cache ───────────────────────────────────────

type fakeCacheConsumer struct {
	invalidatePlanErr error
	invalidatedPlans  []string
}

func (c *fakeCacheConsumer) InvalidatePlan(_ context.Context, planCode string) error {
	c.invalidatedPlans = append(c.invalidatedPlans, planCode)
	return c.invalidatePlanErr
}

func (c *fakeCacheConsumer) GetEffectiveSet(_ context.Context, _ uuid.UUID) (*domain.EffectiveSet, error) {
	return nil, nil
}
func (c *fakeCacheConsumer) SetEffectiveSet(_ context.Context, _ uuid.UUID, _ *domain.EffectiveSet) error {
	return nil
}
func (c *fakeCacheConsumer) InvalidateEffectiveSet(_ context.Context, _ uuid.UUID) error { return nil }
func (c *fakeCacheConsumer) SetQuotaCeiling(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ *int64) error {
	return nil
}
func (c *fakeCacheConsumer) ResetUsageCounter(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ string) error {
	return nil
}
func (c *fakeCacheConsumer) DeleteUsageCounters(_ context.Context, _ domain.LimitKey, _ string) (int64, error) {
	return 0, nil
}
func (c *fakeCacheConsumer) GetPlan(_ context.Context, _ string) (*domain.Plan, error) {
	return nil, nil
}
func (c *fakeCacheConsumer) SetPlan(_ context.Context, _ *domain.Plan) error { return nil }
func (c *fakeCacheConsumer) Health(_ context.Context) error                  { return nil }
func (c *fakeCacheConsumer) Close() error                                    { return nil }

// ── fakeTrialSvc ──────────────────────────────────────────────────────────

type fakeTrialSvc struct {
	purgeErr   error
	purgeCalls []uuid.UUID
}

func (f *fakeTrialSvc) PurgeTrialForTenant(_ context.Context, tenantID uuid.UUID) error {
	f.purgeCalls = append(f.purgeCalls, tenantID)
	return f.purgeErr
}

func (f *fakeTrialSvc) AssignTrialProfile(_ context.Context, _ port.AssignTrialProfileRequest) (*domain.TenantTrialAssignment, error) {
	return nil, nil
}
func (f *fakeTrialSvc) SetTrialOverride(_ context.Context, _ port.SetTrialOverrideRequest) (*domain.TrialEntitlementOverride, error) {
	return nil, nil
}
func (f *fakeTrialSvc) RemoveTrialOverride(_ context.Context, _ port.RemoveTrialOverrideRequest) error {
	return nil
}
func (f *fakeTrialSvc) GetTrialAssignment(_ context.Context, _ uuid.UUID) (*domain.TenantTrialAssignment, error) {
	return nil, nil
}
func (f *fakeTrialSvc) ListTrialOverrides(_ context.Context, _ uuid.UUID) ([]domain.TrialEntitlementOverride, error) {
	return nil, nil
}

// ── nopLogger ─────────────────────────────────────────────────────────────

type nopConsumerLogger struct{}

func (nopConsumerLogger) Info(_ string, _ map[string]any)  {}
func (nopConsumerLogger) Warn(_ string, _ map[string]any)  {}
func (nopConsumerLogger) Error(_ string, _ map[string]any) {}
func (nopConsumerLogger) Debug(_ string, _ map[string]any) {}
