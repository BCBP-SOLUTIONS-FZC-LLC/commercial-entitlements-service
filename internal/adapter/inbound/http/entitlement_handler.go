package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// EntitlementHandler handles read and recompute requests for effective entitlement sets.
type EntitlementHandler struct {
	effectSvc port.EffectiveSetService
}

// NewEntitlementHandler wires up the EntitlementHandler.
func NewEntitlementHandler(effectSvc port.EffectiveSetService) *EntitlementHandler {
	return &EntitlementHandler{effectSvc: effectSvc}
}

// GetEffectiveSetInternal handles ENT-I1: GET /api/v1/internal/tenants/:id/entitlements
// Called by internal services to retrieve the latest effective set.
//
//	@Summary		Get effective entitlement set (internal)
//	@Tags			entitlements
//	@Produce		json
//	@Param			id		path	string	true	"Tenant UUID"
//	@Success		200		{object}	EffectiveSetResponse
//	@Router			/api/v1/internal/tenants/{id}/entitlements [get]
func (h *EntitlementHandler) GetEffectiveSetInternal(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	es, err := h.effectSvc.GetEffectiveSet(c.Request.Context(), tenantID)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, effectiveSetToResponse(es))
}

// GetEffectiveSetTenant handles ENT-T1: GET /api/v1/tenants/:id/entitlements
// Called by the tenant's own authenticated session.
//
//	@Summary		Get effective entitlement set (tenant)
//	@Tags			entitlements
//	@Produce		json
//	@Param			id		path	string	true	"Tenant UUID"
//	@Success		200		{object}	EffectiveSetResponse
//	@Router			/api/v1/tenants/{id}/entitlements [get]
func (h *EntitlementHandler) GetEffectiveSetTenant(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	es, err := h.effectSvc.GetEffectiveSet(c.Request.Context(), tenantID)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, effectiveSetToResponse(es))
}

// Recompute handles POST /api/v1/internal/tenants/:id/entitlements/recompute
// Triggers a full recomputation of the tenant's effective set.
//
//	@Summary		Force recompute entitlement set
//	@Tags			entitlements
//	@Produce		json
//	@Param			id		path	string	true	"Tenant UUID"
//	@Success		200		{object}	EffectiveSetResponse
//	@Router			/api/v1/internal/tenants/{id}/entitlements/recompute [post]
func (h *EntitlementHandler) Recompute(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	es, err := h.effectSvc.Recompute(c.Request.Context(), tenantID, domain.CauseManualRecompute)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, effectiveSetToResponse(es))
}
