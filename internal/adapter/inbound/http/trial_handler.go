package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// TrialHandler handles trial profile assignment and trial overrides (ENT-O3, ENT-O4).
type TrialHandler struct {
	trialSvc port.TrialService
}

// NewTrialHandler wires up the TrialHandler.
func NewTrialHandler(trialSvc port.TrialService) *TrialHandler {
	return &TrialHandler{trialSvc: trialSvc}
}

// AssignProfile handles PUT /api/v1/operator/tenants/:id/entitlements/trial-profile (ENT-O3).
//
//	@Summary	Assign or update a trial profile
//	@Tags		trial
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Tenant UUID"
//	@Param		body	body		AssignTrialProfileBody	true	"Trial profile assignment"
//	@Success	200		{object}	TrialAssignmentResponse
//	@Failure	400		{object}	map[string]interface{}
//	@Router		/api/v1/operator/tenants/{id}/entitlements/trial-profile [put]
func (h *TrialHandler) AssignProfile(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	actorID := actorFromClaims(c)

	var body AssignTrialProfileBody
	if err := c.ShouldBindJSON(&body); err != nil {
		gincommon.AbortWithProblem(c, gincommon.BindingError(err))
		return
	}

	a, err := h.trialSvc.AssignTrialProfile(c.Request.Context(), port.AssignTrialProfileRequest{
		TenantID: tenantID,
		PlanCode: body.PlanCode,
		Profile:  domain.TrialProfile(body.Profile),
		StartsAt: body.StartsAt,
		EndsAt:   body.EndsAt,
		SetBy:    actorID,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	metrics.IncTrialAssignment(body.Profile)
	c.JSON(http.StatusOK, trialAssignmentToResponse(a))
}

// SetOverride handles PUT /api/v1/operator/tenants/:id/entitlements/trial-overrides/:limitKey (ENT-O4).
//
//	@Summary	Set a trial-scoped limit override
//	@Tags		trial
//	@Accept		json
//	@Produce	json
//	@Param		id			path		string					true	"Tenant UUID"
//	@Param		limitKey	path		string					true	"Limit key"
//	@Param		body		body		SetTrialOverrideBody	true	"Override value (null = unlimited)"
//	@Success	200			{object}	TrialOverrideResponse
//	@Failure	400			{object}	map[string]interface{}
//	@Router		/api/v1/operator/tenants/{id}/entitlements/trial-overrides/{limitKey} [put]
func (h *TrialHandler) SetOverride(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	actorID := actorFromClaims(c)
	key := domain.LimitKey(c.Param("limitKey"))

	var body SetTrialOverrideBody
	if err := c.ShouldBindJSON(&body); err != nil {
		gincommon.AbortWithProblem(c, gincommon.BindingError(err))
		return
	}

	o, err := h.trialSvc.SetTrialOverride(c.Request.Context(), port.SetTrialOverrideRequest{
		TenantID: tenantID,
		LimitKey: key,
		Value:    body.Value,
		SetBy:    actorID,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, trialOverrideToResponse(o))
}

// RemoveOverride handles DELETE /api/v1/operator/tenants/:id/entitlements/trial-overrides/:limitKey.
//
//	@Summary	Remove a trial-scoped limit override
//	@Tags		trial
//	@Param		id			path	string	true	"Tenant UUID"
//	@Param		limitKey	path	string	true	"Limit key"
//	@Success	204
//	@Failure	404	{object}	map[string]interface{}
//	@Router		/api/v1/operator/tenants/{id}/entitlements/trial-overrides/{limitKey} [delete]
func (h *TrialHandler) RemoveOverride(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	key := domain.LimitKey(c.Param("limitKey"))

	err := h.trialSvc.RemoveTrialOverride(c.Request.Context(), port.RemoveTrialOverrideRequest{
		TenantID: tenantID,
		LimitKey: key,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetAssignment handles GET /api/v1/operator/tenants/:id/entitlements/trial-profile.
//
//	@Summary	Get the current trial profile assignment
//	@Tags		trial
//	@Produce	json
//	@Param		id	path		string	true	"Tenant UUID"
//	@Success	200	{object}	TrialAssignmentResponse
//	@Failure	404	{object}	map[string]interface{}
//	@Router		/api/v1/operator/tenants/{id}/entitlements/trial-profile [get]
func (h *TrialHandler) GetAssignment(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	a, err := h.trialSvc.GetTrialAssignment(c.Request.Context(), tenantID)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, trialAssignmentToResponse(a))
}

// PurgeTrial handles DELETE /api/v1/operator/tenants/:id/entitlements/trial-profile.
// Removes all trial data (assignment + overrides) for the tenant.
//
//	@Summary	Purge all trial data for a tenant
//	@Tags		trial
//	@Param		id	path	string	true	"Tenant UUID"
//	@Success	204
//	@Failure	404	{object}	map[string]interface{}
//	@Router		/api/v1/operator/tenants/{id}/entitlements/trial-profile [delete]
func (h *TrialHandler) PurgeTrial(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	if err := h.trialSvc.PurgeTrialForTenant(c.Request.Context(), tenantID); err != nil {
		HandleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListOverrides handles GET /api/v1/operator/tenants/:id/entitlements/trial-overrides.
//
//	@Summary	List trial-scoped limit overrides
//	@Tags		trial
//	@Produce	json
//	@Param		id	path		string	true	"Tenant UUID"
//	@Success	200	{array}		TrialOverrideResponse
//	@Router		/api/v1/operator/tenants/{id}/entitlements/trial-overrides [get]
func (h *TrialHandler) ListOverrides(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	overrides, err := h.trialSvc.ListTrialOverrides(c.Request.Context(), tenantID)
	if err != nil {
		HandleError(c, err)
		return
	}
	resp := make([]TrialOverrideResponse, len(overrides))
	for i := range overrides {
		resp[i] = trialOverrideToResponse(&overrides[i])
	}
	c.JSON(http.StatusOK, resp)
}
