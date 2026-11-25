// Package http — shared fakes for all handler unit tests.
package http

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"

	gincommon "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

func init() { gin.SetMode(gin.TestMode) }

// newCtx creates a minimal gin.Context with tenantID injected.
func newCtx(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, w
}

// withTenantID injects a tenantID as the contextKeyTenantID value.
func withTenantID(c *gin.Context, tenantID uuid.UUID) {
	c.Set(contextKeyTenantID, tenantID)
}

// withOperatorRole injects a gincommon RequestContext with platform_operator role.
func withOperatorRole(c *gin.Context, tenantID uuid.UUID, userID string) {
	rc := &gincommon.RequestContextData{
		TenantID: tenantID.String(),
		UserID:   userID,
		Roles:    []string{RoleOperator},
	}
	c.Set(gincommon.RequestContextKey, rc)
}

// withNoRole injects a gincommon RequestContext without any roles.
func withNoRole(c *gin.Context, tenantID uuid.UUID) {
	rc := &gincommon.RequestContextData{TenantID: tenantID.String(), Roles: []string{}}
	c.Set(gincommon.RequestContextKey, rc)
}

// ── fakeEffectiveSvc ──────────────────────────────────────────────────────

type fakeEffectiveSvc struct {
	getEffSet    *domain.EffectiveSet
	getEffErr    error
	recomputed   *domain.EffectiveSet
	recomputeErr error
}

func (f *fakeEffectiveSvc) GetEffectiveSet(_ context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error) {
	if f.getEffErr != nil {
		return nil, f.getEffErr
	}
	if f.getEffSet != nil {
		return f.getEffSet, nil
	}
	return &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}}, nil
}

func (f *fakeEffectiveSvc) Recompute(_ context.Context, tenantID uuid.UUID, _ domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
	if f.recomputeErr != nil {
		return nil, f.recomputeErr
	}
	if f.recomputed != nil {
		return f.recomputed, nil
	}
	return &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}}, nil
}

// ── fakeOverrideSvc ───────────────────────────────────────────────────────

type fakeOverrideSvc struct {
	listRes   []domain.Override
	listErr   error
	getRes    *domain.Override
	getErr    error
	setRes    *domain.Override
	setErr    error
	removeErr error
	histRes   []domain.OverrideHistory
	histErr   error
}

func (f *fakeOverrideSvc) ListOverrides(_ context.Context, _ uuid.UUID) ([]domain.Override, error) {
	return f.listRes, f.listErr
}
func (f *fakeOverrideSvc) GetOverride(_ context.Context, _ uuid.UUID, _ domain.LimitKey) (*domain.Override, error) {
	return f.getRes, f.getErr
}
func (f *fakeOverrideSvc) SetOverride(_ context.Context, _ port.SetOverrideRequest) (*domain.Override, error) {
	return f.setRes, f.setErr
}
func (f *fakeOverrideSvc) RemoveOverride(_ context.Context, _ port.RemoveOverrideRequest) error {
	return f.removeErr
}
func (f *fakeOverrideSvc) ListOverrideHistory(_ context.Context, _ uuid.UUID, _ domain.LimitKey) ([]domain.OverrideHistory, error) {
	return f.histRes, f.histErr
}
func (f *fakeOverrideSvc) ExpireOverrides(_ context.Context) (int, error) { return 0, nil }

// ── fakeAddonSvc ──────────────────────────────────────────────────────────

type fakeAddonSvc struct {
	listRes       []domain.QuotaAddon
	listErr       error
	addRes        *domain.QuotaAddon
	addErr        error
	removeErr     error
	drawdownsRes  []domain.QuotaAddonDrawdown
	drawdownsErr  error
	recordRes     *domain.QuotaAddonDrawdown
	recordErr     error
	shortfallsRes []domain.QuotaAddonShortfall
	shortfallsErr error
	resolveRes    *domain.QuotaAddonShortfall
	resolveErr    error
}

func (f *fakeAddonSvc) ListAddons(_ context.Context, _ uuid.UUID) ([]domain.QuotaAddon, error) {
	return f.listRes, f.listErr
}
func (f *fakeAddonSvc) AddAddon(_ context.Context, _ port.AddAddonRequest) (*domain.QuotaAddon, error) {
	return f.addRes, f.addErr
}
func (f *fakeAddonSvc) RemoveAddon(_ context.Context, _ port.RemoveAddonRequest) error {
	return f.removeErr
}
func (f *fakeAddonSvc) ListDrawdowns(_ context.Context, _ uuid.UUID, _ domain.LimitKey) ([]domain.QuotaAddonDrawdown, error) {
	return f.drawdownsRes, f.drawdownsErr
}
func (f *fakeAddonSvc) RecordDrawdown(_ context.Context, _ port.RecordDrawdownRequest) (*domain.QuotaAddonDrawdown, error) {
	return f.recordRes, f.recordErr
}
func (f *fakeAddonSvc) ListShortfalls(_ context.Context, _ uuid.UUID, _ []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error) {
	return f.shortfallsRes, f.shortfallsErr
}
func (f *fakeAddonSvc) ResolveShortfall(_ context.Context, _ port.ResolveShortfallRequest) (*domain.QuotaAddonShortfall, error) {
	return f.resolveRes, f.resolveErr
}
func (f *fakeAddonSvc) GetShortfall(_ context.Context, _ uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	return nil, nil
}
func (f *fakeAddonSvc) ApplyBillingPurchase(_ context.Context, _ port.ApplyBillingPurchaseRequest) error {
	return nil
}
func (f *fakeAddonSvc) ApplyBillingRefund(_ context.Context, _ port.ApplyBillingRefundRequest) error {
	return nil
}
func (f *fakeAddonSvc) ExpireAddons(_ context.Context) (int, error) { return 0, nil }

// ── fakeTrialSvc ──────────────────────────────────────────────────────────

type fakeTrialSvc struct {
	assignRes    *domain.TenantTrialAssignment
	assignErr    error
	getAssignRes *domain.TenantTrialAssignment
	getAssignErr error
	purgeErr     error
	listOvRes    []domain.TrialEntitlementOverride
	listOvErr    error
	setOvRes     *domain.TrialEntitlementOverride
	setOvErr     error
	removeOvErr  error
}

func (f *fakeTrialSvc) AssignTrialProfile(_ context.Context, _ port.AssignTrialProfileRequest) (*domain.TenantTrialAssignment, error) {
	return f.assignRes, f.assignErr
}
func (f *fakeTrialSvc) GetTrialAssignment(_ context.Context, _ uuid.UUID) (*domain.TenantTrialAssignment, error) {
	return f.getAssignRes, f.getAssignErr
}
func (f *fakeTrialSvc) PurgeTrialForTenant(_ context.Context, _ uuid.UUID) error {
	return f.purgeErr
}
func (f *fakeTrialSvc) ListTrialOverrides(_ context.Context, _ uuid.UUID) ([]domain.TrialEntitlementOverride, error) {
	return f.listOvRes, f.listOvErr
}
func (f *fakeTrialSvc) SetTrialOverride(_ context.Context, _ port.SetTrialOverrideRequest) (*domain.TrialEntitlementOverride, error) {
	return f.setOvRes, f.setOvErr
}
func (f *fakeTrialSvc) RemoveTrialOverride(_ context.Context, _ port.RemoveTrialOverrideRequest) error {
	return f.removeOvErr
}

// ── fakeCatalogSvc ────────────────────────────────────────────────────────

type fakeCatalogSvc struct {
	getPlanRes   *domain.Plan
	getPlanErr   error
	listPlansRes []string
	listPlansErr error
	refreshErr   error
	fanoutErr    error
}

func (f *fakeCatalogSvc) GetPlan(_ context.Context, planCode string) (*domain.Plan, error) {
	if f.getPlanErr != nil {
		return nil, f.getPlanErr
	}
	if f.getPlanRes != nil {
		return f.getPlanRes, nil
	}
	return &domain.Plan{Code: planCode}, nil
}
func (f *fakeCatalogSvc) RefreshPlan(_ context.Context, planCode string) (*domain.Plan, error) {
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	return &domain.Plan{Code: planCode}, nil
}
func (f *fakeCatalogSvc) FanoutCatalogRefresh(_ context.Context, _ string) error { return f.fanoutErr }
func (f *fakeCatalogSvc) ListPlans(_ context.Context) ([]string, error) {
	return f.listPlansRes, f.listPlansErr
}

// ── fakePinger ────────────────────────────────────────────────────────────

type fakePinger struct{ err error }

func (p *fakePinger) Health(_ context.Context) error { return p.err }

// ── bodyJSON helper ───────────────────────────────────────────────────────

func bodyJSON(t interface{}) *httptest.ResponseRecorder { return nil } // placeholder; not used directly

// httpStatus returns the recorded HTTP status code.
func httpStatus(w *httptest.ResponseRecorder) int { return w.Code }

// nextCalled tracks whether gin.Next() was called in middleware tests.
type nextTracker struct{ called bool }

func (n *nextTracker) next(c *gin.Context) { n.called = true; c.Next() }

// passthroughRouter builds a minimal gin engine for middleware integration tests.
func passthroughRouter(middlewares ...gin.HandlerFunc) (*gin.Engine, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	r := gin.New()
	r.Use(middlewares...)
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r, w
}
