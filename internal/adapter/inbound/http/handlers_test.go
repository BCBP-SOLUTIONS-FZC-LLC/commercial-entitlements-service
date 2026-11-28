package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// newHandlerCtx sets up a gin.Context with tenant_id and optional operator role.
func newHandlerCtx(method, path string, tenantID uuid.UUID, body any) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var bodyBuf *bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		bodyBuf = bytes.NewBuffer(b)
	} else {
		bodyBuf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, bodyBuf)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	withTenantID(c, tenantID)
	return c, w
}

// ── EntitlementHandler ────────────────────────────────────────────────────

func TestEntitlementHandler_GetEffectiveSetInternal_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEntitlementHandler_GetEffectiveSetInternal_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{getEffErr: domain.NewError(domain.ErrSnapshotNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetInternal(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestEntitlementHandler_GetEffectiveSetTenant_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetEffectiveSetTenant(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEntitlementHandler_Recompute_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEntitlementHandler_Recompute_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewEntitlementHandler(&fakeEffectiveSvc{recomputeErr: errors.New("internal error")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Recompute(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── OverrideHandler ───────────────────────────────────────────────────────

func TestOverrideHandler_List_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{listRes: []domain.Override{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOverrideHandler_List_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{listErr: errors.New("db error")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestOverrideHandler_Get_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(100)
	h := NewOverrideHandler(&fakeOverrideSvc{getRes: &domain.Override{TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.Get(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOverrideHandler_Get_NotFound(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{getErr: domain.NewError(domain.ErrOverrideNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.Get(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOverrideHandler_Set_200(t *testing.T) {
	tenantID := uuid.New()
	val := int64(50)
	h := NewOverrideHandler(&fakeOverrideSvc{setRes: &domain.Override{TenantID: tenantID, Value: &val}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 50, "reason": "test"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Set(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOverrideHandler_Set_BindError(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.Set(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOverrideHandler_Remove_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "cleanup"})
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestOverrideHandler_Remove_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{removeErr: domain.NewError(domain.ErrOverrideNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOverrideHandler_History_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewOverrideHandler(&fakeOverrideSvc{histRes: []domain.OverrideHistory{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.History(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── AddonHandler ──────────────────────────────────────────────────────────

func TestAddonHandler_List_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{listRes: []domain.QuotaAddon{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAddonHandler_Add_201(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{addRes: &domain.QuotaAddon{ID: uuid.New(), TenantID: tenantID, TotalTokens: 1000, CreatedAt: now, UpdatedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key":    "llm_token_quota_monthly",
		"total_tokens": 1000,
		"reason":       "top-up",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddonHandler_Add_BindError(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.Add(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddonHandler_Add_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{addErr: domain.NewError(domain.ErrTokensRequired, "tokens required")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "total_tokens": -1, "reason": "test",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Add(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddonHandler_Remove_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "cleanup"})
	c.Params = gin.Params{{Key: "addonId", Value: uuid.New().String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Remove(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAddonHandler_Remove_InvalidAddonID(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, map[string]any{"reason": "cleanup"})
	c.Params = gin.Params{{Key: "addonId", Value: "not-a-uuid"}}
	h.Remove(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddonHandler_Remove_BindError(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "addonId", Value: uuid.New().String()}}
	h.Remove(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddonHandler_ListDrawdowns_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{drawdownsRes: []domain.QuotaAddonDrawdown{}})
	c, w := newHandlerCtx(http.MethodGet, "/?limit_key=llm_token_quota_monthly", tenantID, nil)
	h.ListDrawdowns(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAddonHandler_RecordDrawdown_201(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewAddonHandler(&fakeAddonSvc{recordRes: &domain.QuotaAddonDrawdown{ID: uuid.New(), TenantID: tenantID, RecordedAt: now}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key":       "llm_token_quota_monthly",
		"tokens_consumed": 100,
		"period_start":    now.Format(time.RFC3339),
	})
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddonHandler_RecordDrawdown_BindError(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddonHandler_RecordDrawdown_InvalidRequestID(t *testing.T) {
	tenantID := uuid.New()
	h := NewAddonHandler(&fakeAddonSvc{})
	badReqID := "not-a-uuid"
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"limit_key": "llm_token_quota_monthly", "tokens_consumed": 100,
		"period_start": time.Now().UTC().Format(time.RFC3339), "request_id": &badReqID,
	})
	h.RecordDrawdownInternal(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── ShortfallHandler ──────────────────────────────────────────────────────

func TestShortfallHandler_List_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsRes: []domain.QuotaAddonShortfall{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestShortfallHandler_List_WithStatus_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{shortfallsRes: []domain.QuotaAddonShortfall{}})
	c, w := newHandlerCtx(http.MethodGet, "/?status=pending_approval", tenantID, nil)
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestShortfallHandler_Resolve_200(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	sfID := uuid.New()
	disp := domain.DispositionApprove
	ref := "PO-123"
	h := NewShortfallHandler(&fakeAddonSvc{resolveRes: &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: tenantID, Status: domain.ShortfallStatusApproved,
		Disposition: &disp, DispositionRef: &ref, CreatedAt: now, UpdatedAt: now,
	}})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, map[string]any{
		"disposition": "approve", "disposition_ref": &ref,
	})
	c.Params = gin.Params{{Key: "shortfallId", Value: sfID.String()}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.Resolve(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestShortfallHandler_Resolve_InvalidShortfallID(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "shortfallId", Value: "not-a-uuid"}}
	h.Resolve(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestShortfallHandler_Resolve_BindError(t *testing.T) {
	tenantID := uuid.New()
	h := NewShortfallHandler(&fakeAddonSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "shortfallId", Value: uuid.New().String()}}
	h.Resolve(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── TrialHandler ──────────────────────────────────────────────────────────

func TestTrialHandler_AssignProfile_200(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	profile := "full_plan"
	h := NewTrialHandler(&fakeTrialSvc{assignRes: &domain.TenantTrialAssignment{
		TenantID: tenantID, Profile: domain.TrialProfileFullPlan,
		StartsAt: now, EndsAt: now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{
		"plan_code": "starter",
		"profile":   profile,
		"starts_at": now.Format(time.RFC3339),
		"ends_at":   now.Add(24 * time.Hour).Format(time.RFC3339),
		"reason":    "partner evaluation",
	})
	withOperatorRole(c, tenantID, uuid.New().String())
	h.AssignProfile(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTrialHandler_AssignProfile_BindError(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, nil)
	h.AssignProfile(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTrialHandler_GetAssignment_200(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	h := NewTrialHandler(&fakeTrialSvc{getAssignRes: &domain.TenantTrialAssignment{
		TenantID: tenantID, CreatedAt: now, UpdatedAt: now,
	}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetAssignment(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTrialHandler_GetAssignment_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{getAssignErr: domain.NewError(domain.ErrTrialAssignmentNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetAssignment(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTrialHandler_PurgeTrial_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	h.PurgeTrial(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestTrialHandler_ListOverrides_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{listOvRes: []domain.TrialEntitlementOverride{}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListOverrides(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTrialHandler_SetOverride_200(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	val := int64(99)
	h := NewTrialHandler(&fakeTrialSvc{setOvRes: &domain.TrialEntitlementOverride{
		TenantID: tenantID, LimitKey: domain.LimitKeyAPIRateLimitRPS, Value: &val, CreatedAt: now, UpdatedAt: now,
	}})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, map[string]any{"value": 99})
	c.Params = gin.Params{{Key: "limitKey", Value: "api_rate_limit_rps"}}
	withOperatorRole(c, tenantID, uuid.New().String())
	h.SetOverride(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTrialHandler_SetOverride_BindError(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	c, w := newHandlerCtx(http.MethodPut, "/", tenantID, "bad-json-string")
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.SetOverride(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTrialHandler_RemoveOverride_204(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "api_rate_limit_rps"}}
	h.RemoveOverride(c)
	c.Writer.WriteHeaderNow()
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestTrialHandler_RemoveOverride_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewTrialHandler(&fakeTrialSvc{removeOvErr: domain.NewError(domain.ErrTrialOverrideNotFound, "not found")})
	c, w := newHandlerCtx(http.MethodDelete, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "limitKey", Value: "tender_limit"}}
	h.RemoveOverride(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── actorFromClaims ───────────────────────────────────────────────────────

func TestActorFromClaims_NoRC_ReturnsNil(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, uuid.Nil, actorFromClaims(c))
}

func TestActorFromClaims_ValidUserID_ParsesUUID(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	withOperatorRole(c, tenantID, userID.String())
	got := actorFromClaims(c)
	assert.Equal(t, userID, got)
}

func TestActorFromClaims_InvalidUserID_ReturnsNil(t *testing.T) {
	tenantID := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	withOperatorRole(c, tenantID, "not-a-uuid")
	got := actorFromClaims(c)
	assert.Equal(t, uuid.Nil, got)
}

// ── PlansHandler ──────────────────────────────────────────────────────────

func TestPlansHandler_ListPlans_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{listPlansRes: []string{"starter", "pro"}})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.ListPlans(c)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		PlanCodes []string `json:"plan_codes"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.ElementsMatch(t, []string{"starter", "pro"}, resp.PlanCodes)
}

func TestPlansHandler_GetCurrentPlan_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{getPlanRes: &domain.Plan{Code: "starter"}})
	c, w := newHandlerCtx(http.MethodGet, "/?plan_code=starter", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPlansHandler_GetCurrentPlan_MissingPlanCode(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodGet, "/", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlansHandler_GetCurrentPlan_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{getPlanErr: errors.New("iam down")})
	c, w := newHandlerCtx(http.MethodGet, "/?plan_code=starter", tenantID, nil)
	h.GetCurrentPlan(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPlansHandler_RefreshCatalog_200(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "planCode", Value: "starter"}}
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPlansHandler_RefreshCatalog_ServiceError(t *testing.T) {
	tenantID := uuid.New()
	h := NewPlansHandler(&fakeCatalogSvc{refreshErr: errors.New("iam down")})
	c, w := newHandlerCtx(http.MethodPost, "/", tenantID, nil)
	c.Params = gin.Params{{Key: "planCode", Value: "starter"}}
	h.RefreshCatalog(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
