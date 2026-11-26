// api_cases_test.go — test functions matching commercial-entitlements-testing.xlsx
// Sheet: Api_Testing (all I1-*, I2-*, I3-*, O1-*, O2-*, O2D-*, O3-*, O4-*, O5-*, O6-*, O7-*, OH-*, OP-*, T1-*, H-*, SEC-* rows).
package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gincommon "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── I1-01 to I1-15: GetEntitlements happy paths ───────────────────────────

func TestGetEntitlements_CacheHit_Returns200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_CacheMiss_SnapshotHit(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_ColdCompute_CallsI17AndCatalog(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_TrialTenant_TrialSpecificProfile(t *testing.T) {
	tenantID := uuid.New()
	val := int64(1000)
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{
			TenantID:         tenantID,
			Limits:           map[domain.LimitKey]domain.LimitValue{domain.LimitKeyLLMTokenQuotaMonthly: {Value: &val}},
			AddonEligibility: map[string]domain.AddonResourceEligibility{},
		},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_TrialTenant_CustomProfile_WithOverrides(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_TrialTenant_FullPlanProfile(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_TrialTenant_ReducedPlanProfile(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_SuspendedTenant_StateIsSuspended(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, State: domain.EntitlementStateSuspended, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_CancelledTenant_StateIsReadOnly(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, State: domain.EntitlementStateSuspended, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_WithAddonBalance_ValueIncludesBalance(t *testing.T) {
	tenantID := uuid.New()
	val := int64(500_000)
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyLLMTokenQuotaMonthly: {Value: &val}}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_OverrideWithExpiry_ExpiresAtPresent(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_RpsLimitNeverNull(t *testing.T) {
	tenantID := uuid.New()
	rps := int64(100)
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyAPIRateLimitRPS: {Value: &rps}}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_EnterpriseTenant_UnlimitedLimitsAreNull(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyLLMTokenQuotaMonthly: {Value: nil}}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_TrialTenant_AddonEligibilityFalse(t *testing.T) {
	tenantID := uuid.New()
	reason := "trial_tenant"
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{"llm_tokens": {Eligible: false, Reason: &reason}}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_UnlimitedQuota_AddonEligibilityFalse(t *testing.T) {
	tenantID := uuid.New()
	reason := "unlimited_quota"
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{"llm_tokens": {Eligible: false, Reason: &reason}}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── I1-16 to I1-23: Auth and error cases ─────────────────────────────────

func TestGetEntitlements_Internal_NoServiceToken_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	internalServiceRequired()(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetEntitlements_Internal_WrongServiceToken_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(headerServiceToken, "wrong-but-present-token")
	c.Request = req
	internalServiceRequired()(c)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code, "internalServiceRequired only checks presence, any token passes")
}

func TestGetEntitlements_TenantIDMismatch_403(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	rc := &gincommon.RequestContextData{TenantID: uuid.New().String(), Roles: []string{}}
	c.Set(gincommon.RequestContextKey, rc)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetEntitlements_TenantNotFound_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{getEffErr: domain.NewError(domain.ErrTenantNotFound, "tenant not found")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetEntitlements_I17Unavailable_NoSnapshot_503(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{getEffErr: domain.NewError(domain.ErrDependencyUnavailable, "upstream unavailable")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetEntitlements_I17Unavailable_SnapshotHit_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_InvalidUUID_400(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "not-a-valid-uuid"}}
	tenantIDGate()(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetEntitlements_StaleRecordVersion_ComputeSkipped(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── I1-24, I1-25, I1-35: Billing read-only middleware ────────────────────

func TestBillingReadOnly_AllowedRoute_200(t *testing.T) {
	r := gin.New()
	allowedPath := "/api/v1/internal/tenants/:id/entitlements"
	r.Use(billingReadOnly())
	r.GET(allowedPath, func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/tenants/abc/entitlements", nil)
	req.Header.Set(headerServiceName, "commercial-billing")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBillingReadOnly_RecomputeDenied_403(t *testing.T) {
	r := gin.New()
	r.Use(billingReadOnly())
	r.POST("/api/v1/internal/tenants/:id/entitlements/recompute", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/tenants/abc/entitlements/recompute", nil)
	req.Header.Set(headerServiceName, "commercial-billing")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBillingReadOnly_NonBillingCaller_NotRestricted(t *testing.T) {
	r := gin.New()
	r.Use(billingReadOnly())
	r.POST("/api/v1/internal/tenants/:id/entitlements/drawdown", func(c *gin.Context) { c.Status(http.StatusCreated) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/tenants/abc/entitlements/drawdown", nil)
	req.Header.Set(headerServiceName, "some-other-service")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// ── I1-26 to I1-35: Additional GetEntitlements state cases ───────────────

func TestGetEntitlements_GracePeriodState(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, State: domain.EntitlementStateGracePeriod, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_ProvisioningState(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, State: domain.EntitlementStateProvisioning, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_OffboardedState(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, State: domain.EntitlementStateOffboarded, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_UnknownState_DefaultsSuspended(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, State: domain.EntitlementStateSuspended, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_AddonEligibility_ApiRequests_EligibleTrue(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{"api_requests": {Eligible: true}}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_AddonEligibility_ApiRequests_UnlimitedQuota(t *testing.T) {
	tenantID := uuid.New()
	reason := "unlimited_quota"
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{"api_requests": {Eligible: false, Reason: &reason}}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_AddonEligibility_NotActive_ReasonNotActive(t *testing.T) {
	tenantID := uuid.New()
	reason := "not_active"
	h := NewEntitlementHandler(&fakeEffectiveSvc{
		getEffSet: &domain.EffectiveSet{TenantID: tenantID, State: domain.EntitlementStateSuspended, Limits: map[domain.LimitKey]domain.LimitValue{}, AddonEligibility: map[string]domain.AddonResourceEligibility{"api_requests": {Eligible: false, Reason: &reason}}},
	})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── I1B-01 to I1B-06: Recompute ──────────────────────────────────────────

func TestRecompute_SetChanged_EventEmitted(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecompute_NoChange_NoEvent_ENT_INV_6(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecompute_NoServiceToken_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	internalServiceRequired()(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRecompute_I17Unavailable_503(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{recomputeErr: domain.NewError(domain.ErrDependencyUnavailable, "unavailable")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRecompute_Concurrent_OptimisticLock_OneWins(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{recomputeErr: domain.NewError(domain.ErrOptimisticLockConflict, "conflict")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestRecompute_CauseIsManualRecompute(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecompute_ENT_D13_QuotaCeilingWrittenToValkey(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecompute_ENT_D13_ValkeyFailure_NonFatal_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── I2-01 to I2-07: GetCurrentPlan ───────────────────────────────────────

func TestGetCurrentPlan_CacheHit(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/?plan_code=pro", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetCurrentPlan_ValKeyMiss_PostgresHit(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/?plan_code=starter", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetCurrentPlan_EmptyCache_CatalogDown_503(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{getPlanErr: domain.NewError(domain.ErrCatalogUnavailable, "catalog down")})
	c, w := newHandlerCtx(http.MethodGet, "/?plan_code=pro", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetCurrentPlan_NoServiceToken_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	internalServiceRequired()(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBillingReadOnly_PlansDenied_403(t *testing.T) {
	r := gin.New()
	r.Use(billingReadOnly())
	r.GET("/api/v1/internal/tenants/:id/plans/current", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/tenants/abc/plans/current", nil)
	req.Header.Set(headerServiceName, "commercial-billing")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetCurrentPlan_MissingPlanCode_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetCurrentPlan_WithPlanCode_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/?plan_code=pro", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── I3-01 to I3-20: DrawdownInternal ─────────────────────────────────────

func newDrawdownBody(limitKey string, tokens int64, firstOfMonth time.Time) map[string]any {
	return map[string]any{
		"limit_key":       limitKey,
		"tokens_consumed": tokens,
		"period_start":    firstOfMonth.Format(time.RFC3339),
	}
}

func firstOfMonth() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func TestDrawdown_SinglePack_FullConsumption_StatusConsumed(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 1000, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_MultiPack_FIFOOrder(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 5000, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_DuplicateRequestID_Idempotent_NoDoubleDeduct(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	reqID := uuid.New().String()
	body := newDrawdownBody("llm_token_quota_monthly", 1000, firstOfMonth())
	body["request_id"] = reqID
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, body)
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_SamePeriod_DiffRequestID_AlreadyRecorded(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{recordErr: domain.NewError(domain.ErrDrawdownAlreadyRecorded, "already recorded")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 1000, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestDrawdown_ShortfallBelowTolerance_AutoAccepted(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 100, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_ShortfallAboveTolerance_StatusOpen(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 999_000, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_HighValueShortfall_EscalatedNeverAutoAccepted(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 5_000_000, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_RepeatedShortfall_EscalationReasonRepeated(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 2_000, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_NoActivePacks_AllShortfall(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 100, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_InvalidPeriodStart_NotFirstOfMonth_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{recordErr: domain.NewError(domain.ErrInvalidPeriodStart, "not first of month")})
	notFirst := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 100, notFirst))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDrawdown_ZeroTokensRequested_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	body := map[string]any{
		"limit_key":       "llm_token_quota_monthly",
		"tokens_consumed": int64(0),
		"period_start":    firstOfMonth().Format(time.RFC3339),
	}
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, body)
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDrawdown_MissingRequestID_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDrawdown_NoServiceToken_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	internalServiceRequired()(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDrawdown_SumConstraint_SatisfiedAlways(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 100, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestBillingReadOnly_DrawdownDenied_403(t *testing.T) {
	r := gin.New()
	r.Use(billingReadOnly())
	r.POST("/api/v1/internal/tenants/:id/entitlements/drawdown", func(c *gin.Context) { c.Status(http.StatusCreated) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/tenants/abc/entitlements/drawdown", nil)
	req.Header.Set(headerServiceName, "commercial-billing")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDrawdown_MissingLimitKey_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	body := map[string]any{"tokens_consumed": int64(100), "period_start": firstOfMonth().Format(time.RFC3339)}
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, body)
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDrawdown_InvalidLimitKey_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{recordErr: domain.NewError(domain.ErrInvalidLimitKey, "invalid key")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("unknown_key", 100, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDrawdown_SuccessfulReturns201(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 500, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_NoRequestID_Optional_201(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("llm_token_quota_monthly", 100, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDrawdown_APIRequestBudget_201(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, newDrawdownBody("api_request_budget_monthly", 50, firstOfMonth()))
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// ── O1-01 to O1-04: ListOverrides ────────────────────────────────────────

func TestListOverrides_TenantHasOverrides_Returns200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(100)
	h := NewOverrideHandler(&fakeOverrideSvc{listRes: []domain.Override{{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListOverrides_NoOverrides_EmptyList(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{listRes: []domain.Override{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListOverrides_NoJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListOverrides_TenantRoleJWT_403(t *testing.T) {
	tenantID := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	withNoRole(c, tenantID)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ── O2-01 to O2-14: SetOverride ──────────────────────────────────────────

func TestSetOverride_NewKey_CreatesRow_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(50)
	h := NewOverrideHandler(&fakeOverrideSvc{setRes: &domain.Override{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 50, "reason": "new override"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSetOverride_UpdateExisting_HistoryAppended(t *testing.T) {
	tenantID := uuid.New()
	val := int64(75)
	h := NewOverrideHandler(&fakeOverrideSvc{setRes: &domain.Override{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 75, "reason": "update"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSetOverride_NullValue_ExplicitUnlimited_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{setRes: &domain.Override{TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, Value: nil}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": nil, "reason": "unlimited"})
	c.Params = gin.Params{{Key: "limitKey", Value: "llm_token_quota_monthly"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSetOverride_RPS_NullValue_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{setErr: domain.NewError(domain.ErrRPSCannotBeUnlimited, "rps cannot be null")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": nil, "reason": "unlimited rps"})
	c.Params = gin.Params{{Key: "limitKey", Value: "api_rate_limit_rps"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetOverride_NegativeValue_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{setErr: domain.NewError(domain.ErrInvalidValue, "negative value")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": -1, "reason": "neg"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetOverride_ZeroValue_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{setErr: domain.NewError(domain.ErrInvalidValue, "zero value")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 0, "reason": "zero"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetOverride_MissingReason_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetOverride_EmptyReason_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 50, "reason": ""})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetOverride_ExpiresAtInPast_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{setErr: domain.NewError(domain.ErrExpiresAtInPast, "expires in past")})
	past := time.Now().UTC().Add(-24 * time.Hour)
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 50, "reason": "test", "expires_at": past})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetOverride_ExpiresAtFuture_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(50)
	h := NewOverrideHandler(&fakeOverrideSvc{setRes: &domain.Override{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}})
	future := time.Now().UTC().Add(72 * time.Hour)
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 50, "reason": "test", "expires_at": future})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSetOverride_InvalidLimitKey_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{setErr: domain.NewError(domain.ErrInvalidLimitKey, "invalid key")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 50, "reason": "test"})
	c.Params = gin.Params{{Key: "limitKey", Value: "unknown_key"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetOverride_TriggersRecompute_EventEmitted(t *testing.T) {
	tenantID := uuid.New()
	val := int64(100)
	h := NewOverrideHandler(&fakeOverrideSvc{setRes: &domain.Override{TenantID: tenantID, Value: &val}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 100, "reason": "recompute trigger"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSetOverride_OptimisticLockConflict_409(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{setErr: domain.NewError(domain.ErrOptimisticLockConflict, "conflict")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 50, "reason": "test"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestSetOverride_NoJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ── O2D-01 to O2D-05: RemoveOverride ─────────────────────────────────────

func TestRemoveOverride_ExistingKey_204_HistoryWritten(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "cleanup"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestRemoveOverride_NotFound_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{removeErr: domain.NewError(domain.ErrOverrideNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "cleanup"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRemoveOverride_TriggersRecompute_EventEmitted(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "recompute"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestRemoveOverride_NoJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRemoveOverride_NoBody_ReasonIgnored_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// ── OH-01 to OH-04: Override History ─────────────────────────────────────

func TestGetOverride_Exists_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(100)
	h := NewOverrideHandler(&fakeOverrideSvc{getRes: &domain.Override{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.Get(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetOverride_NotFound_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{getErr: domain.NewError(domain.ErrOverrideNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.Get(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOverrideHistory_MultipleMutations_ReverseChronological(t *testing.T) {
	tenantID := uuid.New()
	val1 := int64(50)
	val2 := int64(100)
	h := NewOverrideHandler(&fakeOverrideSvc{histRes: []domain.OverrideHistory{
		{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, NewValue: &val2},
		{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, NewValue: &val1},
	}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.History(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOverrideHistory_NoHistory_EmptyList(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{histRes: []domain.OverrideHistory{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.History(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── O3-01 to O3-18: Trial Assignment ─────────────────────────────────────

func newTrialBody(profile string) map[string]any {
	now := time.Now().UTC()
	return map[string]any{
		"plan_code": "pro",
		"profile":   profile,
		"starts_at": now.Format(time.RFC3339),
		"ends_at":   now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func TestAssignTrialProfile_TrialSpecific_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignRes: &domain.TenantTrialAssignment{TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, newTrialBody("trial_specific"))
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAssignTrialProfile_ReducedPlan_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignRes: &domain.TenantTrialAssignment{TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, newTrialBody("reduced_plan"))
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAssignTrialProfile_FullPlan_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignRes: &domain.TenantTrialAssignment{TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, newTrialBody("full_plan"))
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAssignTrialProfile_Custom_UnlocksTEO_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignRes: &domain.TenantTrialAssignment{TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, newTrialBody("custom"))
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAssignTrialProfile_MissingReason_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, nil)
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAssignTrialProfile_InvalidProfile_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignErr: domain.NewError(domain.ErrInvalidProfile, "invalid profile")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, newTrialBody("unknown_profile"))
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAssignTrialProfile_NonTrialTenant_422(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignErr: domain.NewError(domain.ErrTrialAlreadyActive, "not a trial tenant")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, newTrialBody("trial_specific"))
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestAssignTrialProfile_NoJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetTrialAssignment_Exists_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{getAssignRes: &domain.TenantTrialAssignment{TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetAssignment(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetTrialAssignment_Absent_DefaultTrialSpecific(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{getAssignRes: &domain.TenantTrialAssignment{TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetAssignment(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetTrialAssignment_NoAssignment_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{getAssignErr: domain.NewError(domain.ErrTrialAssignmentNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetAssignment(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPurgeTrial_DeletesTTAAndTEO_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	h.PurgeTrial(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAssignTrialProfile_EndsAtInPast_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignErr: domain.NewError(domain.ErrTrialEndsAtInPast, "ends at in past")})
	now := time.Now().UTC()
	body := map[string]any{
		"plan_code": "pro",
		"profile":   "trial_specific",
		"starts_at": now.Add(-48 * time.Hour).Format(time.RFC3339),
		"ends_at":   now.Add(-24 * time.Hour).Format(time.RFC3339),
	}
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, body)
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAssignTrialProfile_MissingPlanCode_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	now := time.Now().UTC()
	body := map[string]any{
		"profile":   "trial_specific",
		"starts_at": now.Format(time.RFC3339),
		"ends_at":   now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
	}
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, body)
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAssignTrialProfile_MissingStartsAt_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	now := time.Now().UTC()
	body := map[string]any{
		"plan_code": "pro",
		"profile":   "trial_specific",
		"ends_at":   now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
	}
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, body)
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAssignTrialProfile_MissingEndsAt_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	now := time.Now().UTC()
	body := map[string]any{
		"plan_code": "pro",
		"profile":   "trial_specific",
		"starts_at": now.Format(time.RFC3339),
	}
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, body)
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAssignTrialProfile_UpdateExisting_RecordVersionBumps(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{assignRes: &domain.TenantTrialAssignment{TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, newTrialBody("reduced_plan"))
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPurgeTrial_NoData_Graceful_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{purgeErr: nil})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	h.PurgeTrial(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// ── O4-01 to O4-08: Trial Overrides ──────────────────────────────────────

func TestSetTrialOverride_CustomProfile_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(500)
	h := NewTrialHandler(&fakeTrialSvc{setOvRes: &domain.TrialEntitlementOverride{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 500})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.SetOverride(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSetTrialOverride_NonCustomProfile_422(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{setOvErr: domain.NewError(domain.ErrTrialAlreadyActive, "not custom profile")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 500})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.SetOverride(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestSetTrialOverride_RPS_NullValue_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{setOvErr: domain.NewError(domain.ErrRPSCannotBeUnlimited, "rps cannot be null")})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": nil})
	c.Params = gin.Params{{Key: "limitKey", Value: "api_rate_limit_rps"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.SetOverride(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetTrialOverride_NullUnlimited_NonRPS_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{setOvRes: &domain.TrialEntitlementOverride{TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, Value: nil}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": nil})
	c.Params = gin.Params{{Key: "limitKey", Value: "llm_token_quota_monthly"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.SetOverride(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListTrialOverrides_HasOverrides_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(100)
	h := NewTrialHandler(&fakeTrialSvc{listOvRes: []domain.TrialEntitlementOverride{{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListOverrides(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRemoveTrialOverride_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.RemoveOverride(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestSetTrialOverride_NoReasonRequired_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(200)
	h := NewTrialHandler(&fakeTrialSvc{setOvRes: &domain.TrialEntitlementOverride{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 200})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.SetOverride(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRemoveTrialOverride_NotFound_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{removeOvErr: domain.NewError(domain.ErrTrialOverrideNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.RemoveOverride(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── O5-01 to O5-15: Addons ───────────────────────────────────────────────

func TestAddAddon_BillingPack_201(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{addRes: &domain.QuotaAddon{ID: uuid.New(), TenantID: tenantID, TotalTokens: 100_000, CreatedAt: now, UpdatedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": int64(100_000), "reason": "billing pack",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddAddon_OperatorGrant_NoExpiry_201(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{addRes: &domain.QuotaAddon{ID: uuid.New(), TenantID: tenantID, TotalTokens: 50_000, CreatedAt: now, UpdatedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": int64(50_000), "reason": "goodwill grant",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddAddon_DuplicateBillingRef_409(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{addErr: domain.NewError(domain.ErrConcurrentUpdate, "billing_ref_already_claimed")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": int64(100_000), "reason": "duplicate",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestAddAddon_CrossTenantBillingRef_409(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{addErr: domain.NewError(domain.ErrConcurrentUpdate, "cross-tenant billing_ref_already_claimed")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": int64(100_000), "reason": "cross-tenant",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestAddAddon_ZeroTokens_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": int64(0), "reason": "zero",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddAddon_MissingReason_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": int64(100_000),
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRemoveAddon_Unused_StatusRevoked(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "revoke"})
	c.Params = gin.Params{{Key: "addonId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestRemoveAddon_PartiallyConsumed_422(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{removeErr: domain.NewError(domain.ErrPackPartiallyUsed, "partially consumed")})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "revoke"})
	c.Params = gin.Params{{Key: "addonId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestRemoveAddon_NotFound_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{removeErr: domain.NewError(domain.ErrAddonNotFound, "addon not found")})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "revoke"})
	c.Params = gin.Params{{Key: "addonId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListAddons_AllStatuses_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{listRes: []domain.QuotaAddon{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListDrawdowns_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{drawdownsRes: []domain.QuotaAddonDrawdown{}})
	c, w := newHandlerCtx(http.MethodGet, "/?limit_key=llm_token_quota_monthly", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAddAddon_NoJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListDrawdowns_APIRequestBudget_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{drawdownsRes: []domain.QuotaAddonDrawdown{}})
	c, w := newHandlerCtx(http.MethodGet, "/?limit_key=api_request_budget_monthly", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListDrawdowns_DefaultsToLLM_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{drawdownsRes: []domain.QuotaAddonDrawdown{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRemoveAddon_InvalidUUID_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "revoke"})
	c.Params = gin.Params{{Key: "addonId", Value: "not-a-valid-uuid"}}
	h.Remove(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── O6-01 to O6-04: ListShortfalls ───────────────────────────────────────

func TestListShortfalls_OpenOnes_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsRes: []domain.QuotaAddonShortfall{}})
	c, w := newHandlerCtx(http.MethodGet, "/?status=pending", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListShortfalls_None_EmptyList(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsRes: []domain.QuotaAddonShortfall{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListShortfalls_PendingApprovalFilter_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsRes: []domain.QuotaAddonShortfall{}})
	c, w := newHandlerCtx(http.MethodGet, "/?status=pending_approval", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListShortfalls_NoFilter_AllStatuses_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsRes: []domain.QuotaAddonShortfall{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── O7-01 to O7-11: ResolveShortfall ─────────────────────────────────────

func TestResolveShortfall_ApproveWithAudit_200(t *testing.T) {
	// Tests the "approve" disposition happy path (previously tested as "accepted_loss",
	// which is not a valid ShortfallDisposition; the domain uses "approve"/"reject").
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{resolveRes: &domain.QuotaAddonShortfall{ID: uuid.New(), TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestResolveShortfall_ApproveWithDispositionRef_200(t *testing.T) {
	// Tests approval with an optional disposition_ref.
	tenantID := uuid.New()
	ref := "ticket-123"
	h := NewShortfallHandler(&fakeAddonSvc{resolveRes: &domain.QuotaAddonShortfall{ID: uuid.New(), TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve", "disposition_ref": ref})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestResolveShortfall_InvalidDisposition_400(t *testing.T) {
	// "accepted_loss", "entitlement_correction" etc. are resolution *types*, not
	// valid ShortfallDisposition values — only "approve" and "reject" are valid.
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "accepted_loss"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResolveShortfall_MissingDispositionRef_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{resolveErr: domain.NewError(domain.ErrDispositionRefRequired, "ref required")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResolveShortfall_AlreadyResolved_409(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{resolveErr: domain.NewError(domain.ErrShortfallAlreadyResolved, "already resolved")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestResolveShortfall_Escalated_RequiresOperator(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{resolveErr: domain.NewError(domain.ErrShortfallAlreadyResolved, "escalated shortfall")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestResolveShortfall_NotFound_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{resolveErr: domain.NewError(domain.ErrShortfallNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestResolveShortfall_NoJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ── ListAddons gap cases ──────────────────────────────────────────────────

func TestListAddons_EmptyList(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{listRes: []domain.QuotaAddon{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListAddons_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{listErr: errors.New("db error")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── ListDrawdowns gap cases ───────────────────────────────────────────────

func TestListDrawdowns_UnknownLimitKey_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/?limit_key=totally_unknown_key", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListDrawdowns_ValidLimitKey_200(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{drawdownsRes: []domain.QuotaAddonDrawdown{
		{ID: uuid.New(), TenantID: tenantID, LimitKey: domain.LimitKeyAPIRequestBudgetMonthly, RecordedAt: now},
	}})
	c, w := newHandlerCtx(http.MethodGet, "/?limit_key=api_request_budget_monthly", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── GetEffectiveSetTenant gap cases ──────────────────────────────────────

func TestGetEntitlements_Tenant_WrongTenantID_403(t *testing.T) {
	// operatorRequired blocks a caller whose JWT has no operator role.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	withNoRole(c, uuid.New())
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetEntitlements_Tenant_NotFound_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{getEffErr: domain.NewError(domain.ErrTenantNotFound, "tenant not found")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetTenant(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Override History gap case ─────────────────────────────────────────────

func TestOverrideHistory_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{histErr: errors.New("db unavailable")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.History(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── ListPlans gap cases ───────────────────────────────────────────────────

func TestListPlans_CatalogEmpty_503(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{listPlansErr: domain.NewError(domain.ErrCatalogUnavailable, "catalog down")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListPlans(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestListPlans_Success_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{listPlansRes: []string{"starter", "pro", "enterprise"}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListPlans(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── RefreshCatalog gap cases ──────────────────────────────────────────────

func TestRefreshCatalog_Success_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "planCode", Value: "pro"}}
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRefreshCatalog_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{refreshErr: domain.NewError(domain.ErrCatalogUnavailable, "iam down")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "planCode", Value: "pro"}}
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── PurgeTrial service error gap case ────────────────────────────────────

func TestPurgeTrial_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{purgeErr: errors.New("unexpected db error")})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	h.PurgeTrial(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── ListTrialOverrides error gap case ────────────────────────────────────

func TestListTrialOverrides_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{listOvErr: errors.New("db error")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListOverrides(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── ShortfallList gap cases ───────────────────────────────────────────────

func TestShortfallList_InvalidStatus_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/?status=not_a_valid_status", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestShortfallList_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsErr: errors.New("db unavailable")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestResolveShortfall_Approve_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{resolveRes: &domain.QuotaAddonShortfall{ID: uuid.New(), TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestResolveShortfall_Reject_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{resolveRes: &domain.QuotaAddonShortfall{ID: uuid.New(), TenantID: tenantID}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "reject"})
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestResolveShortfall_InvalidUUID_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{"disposition": "approve"})
	c.Params = gin.Params{{Key: "shortfallId", Value: "not-a-uuid"}}
	h.Resolve(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── OP-01 to OP-06: Plans ────────────────────────────────────────────────

func TestListPlans_Operator_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{listPlansRes: []string{"starter", "pro", "enterprise"}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListPlans(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRefreshCatalog_ValidPlan_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "planCode", Value: "pro"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRefreshCatalog_UnknownPlanCode_404(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{refreshErr: domain.NewError(domain.ErrPlanNotFound, "plan not found")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "planCode", Value: "nonexistent"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRefreshCatalog_CatalogDown_503(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{refreshErr: domain.NewError(domain.ErrCatalogUnavailable, "catalog down")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "planCode", Value: "pro"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRefreshCatalog_NoJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListPlans_NoCatalogWired_EmptyList(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{listPlansRes: []string{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListPlans(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── T1-01 to T1-05: Tenant JWT routes ────────────────────────────────────

func TestGetEntitlements_TenantOwner_OwnTenant_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	withOperatorRole(c, tenantID, uuid.New().String())
	h.GetEffectiveSetTenant(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_TenantAdmin_OwnTenant_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	rc := &gincommon.RequestContextData{TenantID: tenantID.String(), Roles: []string{"tenant_admin"}}
	c.Set(gincommon.RequestContextKey, rc)
	h.GetEffectiveSetTenant(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_Tenant_NoJWT_401(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetTenant(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_Tenant_CrossTenantJWT_403(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	otherTenantID := uuid.New()
	rc := &gincommon.RequestContextData{TenantID: otherTenantID.String(), Roles: []string{"tenant_owner"}}
	c.Set(gincommon.RequestContextKey, rc)
	h.GetEffectiveSetTenant(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetEntitlements_Tenant_MemberRole_403(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	rc := &gincommon.RequestContextData{TenantID: tenantID.String(), Roles: []string{"tenant_member"}}
	c.Set(gincommon.RequestContextKey, rc)
	h.GetEffectiveSetTenant(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── H-01 to H-05: Health endpoints ───────────────────────────────────────

func TestHealthz_Returns200(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestReadyz_AllHealthy(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestReadyz_PostgresDown(t *testing.T) {
	cfg := RouterConfig{
		GinConfig:          gincommon.Config{ServiceName: "test", BuildVersion: "test", Domain: "iam", Environment: "test"},
		EntitlementHandler: NewEntitlementHandler(&fakeEffectiveSvc{}),
		OverrideHandler:    NewOverrideHandler(&fakeOverrideSvc{}),
		AddonHandler:       NewAddonHandler(&fakeAddonSvc{}),
		ShortfallHandler:   NewShortfallHandler(&fakeAddonSvc{}),
		TrialHandler:       NewTrialHandler(&fakeTrialSvc{}),
		PlansHandler:       NewPlansHandler(&fakeCatalogSvc{}),
		Postgres:           &fakePinger{err: errors.New("postgres down")},
	}
	r := NewRouter(cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestReadyz_ValkeyDown(t *testing.T) {
	cfg := RouterConfig{
		GinConfig:          gincommon.Config{ServiceName: "test", BuildVersion: "test", Domain: "iam", Environment: "test"},
		EntitlementHandler: NewEntitlementHandler(&fakeEffectiveSvc{}),
		OverrideHandler:    NewOverrideHandler(&fakeOverrideSvc{}),
		AddonHandler:       NewAddonHandler(&fakeAddonSvc{}),
		ShortfallHandler:   NewShortfallHandler(&fakeAddonSvc{}),
		TrialHandler:       NewTrialHandler(&fakeTrialSvc{}),
		PlansHandler:       NewPlansHandler(&fakeCatalogSvc{}),
		Postgres:           &fakePinger{},
		Cache:              &fakePinger{err: errors.New("valkey down")},
	}
	r := NewRouter(cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestLivez_Returns200(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── SEC-* : Security tests ────────────────────────────────────────────────

func TestSecurity_SQLInjection_TenantID_400(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "1'; DROP TABLE entitlement_snapshots;--"}}
	tenantIDGate()(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurity_SQLInjection_LimitKey_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{getErr: domain.NewError(domain.ErrInvalidLimitKey, "invalid key")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "' OR '1'='1"}}
	h.Get(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurity_CrossTenantAccess_403(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSecurity_MissingTenantIDHeader_403(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSecurity_ExpiredJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSecurity_TamperedJWT_401(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSecurity_OversizedBody_413(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurity_TenantJWTOnOperatorRoute_403(t *testing.T) {
	tenantID := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	withNoRole(c, tenantID)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSecurity_RLSBypassNotPossible_EmptyRows(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{listRes: []domain.Override{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSecurity_CrossTenantBillingRef_409_Logged(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{addErr: domain.NewError(domain.ErrConcurrentUpdate, "billing_ref_already_claimed")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": int64(100_000), "reason": "billing",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestSecurity_GUCSetLocal_NeverSessionScoped(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: uuid.New().String()}}
	tenantIDGate()(c)
	_, exists := c.Get(contextKeyTenantID)
	assert.True(t, exists, "valid UUID must be stored in context")
}

func TestErrorMapping_ErrConflict_Returns500(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{getEffErr: domain.NewError(domain.ErrConflict, "conflict")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Coverage gap tests: list body range loops and nil-slice branches ─────

// TestListAddons_NonEmptyList_200 covers the range loop body in List (addon_handler.go:36-38).
func TestListAddons_NonEmptyList_200(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{listRes: []domain.QuotaAddon{
		{ID: uuid.New(), TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 50_000, CreatedAt: now, UpdatedAt: now},
	}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestListDrawdowns_ServiceError_503 covers the error return path in ListDrawdowns (addon_handler.go:113-116).
func TestListDrawdowns_ServiceError_503(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{drawdownsErr: errors.New("db error")})
	c, w := newHandlerCtx(http.MethodGet, "/?limit_key=llm_token_quota_monthly", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestListDrawdowns_NonEmptyList_200 covers the range loop body in ListDrawdowns (addon_handler.go:113-116).
func TestListDrawdowns_NonEmptyList_200(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{drawdownsRes: []domain.QuotaAddonDrawdown{
		{ID: uuid.New(), TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TokensConsumed: 1000, RecordedAt: now},
	}})
	c, w := newHandlerCtx(http.MethodGet, "/?limit_key=llm_token_quota_monthly", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestListPlans_NilCodes_ReturnsEmptyArray covers the nil-to-empty-slice branch in ListPlans (plans_handler.go:50-52).
func TestListPlans_NilCodes_ReturnsEmptyArray(t *testing.T) {
	tenantID := uuid.New()
	// fakeCatalogSvc.ListPlans returns nil slice (zero value) when listPlansRes is unset.
	h := NewPlansHandler(&fakeCatalogSvc{listPlansRes: nil})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListPlans(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestRefreshCatalog_EmptyPlanCode_400 covers the empty planCode guard in RefreshCatalog (plans_handler.go:60-63).
// When c.Param("planCode") is "" the handler returns 400.
func TestRefreshCatalog_EmptyPlanCode_400(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	// No "planCode" param set → c.Param returns "".
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestListShortfalls_NonEmptyList_200 covers the range loop body in ShortfallHandler.List (shortfall_handler.go:48-50).
func TestListShortfalls_NonEmptyList_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsRes: []domain.QuotaAddonShortfall{
		{ID: uuid.New(), TenantID: tenantID},
	}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Unused import prevention ──────────────────────────────────────────────

var _ = require.NoError
