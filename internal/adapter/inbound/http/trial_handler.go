package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

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
	c.JSON(http.StatusOK, trialAssignmentToResponse(a))
}

// SetOverride handles PUT /api/v1/operator/tenants/:id/entitlements/trial-overrides/:limitKey (ENT-O4).
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
