package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// OverrideHandler handles operator override CRUD (ENT-O1, ENT-O2).
type OverrideHandler struct {
	overrideSvc port.OverrideService
}

// NewOverrideHandler wires up the OverrideHandler.
func NewOverrideHandler(overrideSvc port.OverrideService) *OverrideHandler {
	return &OverrideHandler{overrideSvc: overrideSvc}
}

// List handles GET /api/v1/operator/tenants/:id/entitlements/overrides (ENT-O1).
func (h *OverrideHandler) List(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	overrides, err := h.overrideSvc.ListOverrides(c.Request.Context(), tenantID)
	if err != nil {
		HandleError(c, err)
		return
	}
	resp := make([]OverrideResponse, len(overrides))
	for i := range overrides {
		resp[i] = overrideToResponse(&overrides[i])
	}
	c.JSON(http.StatusOK, resp)
}

// Get handles GET /api/v1/operator/tenants/:id/entitlements/overrides/:limitKey.
func (h *OverrideHandler) Get(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	key := domain.LimitKey(c.Param("limitKey"))
	o, err := h.overrideSvc.GetOverride(c.Request.Context(), tenantID, key)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, overrideToResponse(o))
}

// Set handles PUT /api/v1/operator/tenants/:id/entitlements/overrides/:limitKey (ENT-O2).
func (h *OverrideHandler) Set(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	key := domain.LimitKey(c.Param("limitKey"))
	actorID := actorFromClaims(c)

	var body SetOverrideBody
	if err := c.ShouldBindJSON(&body); err != nil {
		gincommon.AbortWithProblem(c, gincommon.BindingError(err))
		return
	}

	o, err := h.overrideSvc.SetOverride(c.Request.Context(), port.SetOverrideRequest{
		TenantID:  tenantID,
		LimitKey:  key,
		Value:     body.Value,
		Reason:    body.Reason,
		SetBy:     actorID,
		ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	metrics.IncOverrideSet(key)
	c.JSON(http.StatusOK, overrideToResponse(o))
}

// Remove handles DELETE /api/v1/operator/tenants/:id/entitlements/overrides/:limitKey.
func (h *OverrideHandler) Remove(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	key := domain.LimitKey(c.Param("limitKey"))
	actorID := actorFromClaims(c)

	var body RemoveOverrideBody
	_ = c.ShouldBindJSON(&body) // reason is optional on DELETE

	err := h.overrideSvc.RemoveOverride(c.Request.Context(), port.RemoveOverrideRequest{
		TenantID:  tenantID,
		LimitKey:  key,
		RemovedBy: actorID,
		Reason:    body.Reason,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	metrics.IncOverrideRemove(key)
	c.Status(http.StatusNoContent)
}

// History handles GET /api/v1/operator/tenants/:id/entitlements/overrides/:limitKey/history.
func (h *OverrideHandler) History(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	key := domain.LimitKey(c.Param("limitKey"))
	hist, err := h.overrideSvc.ListOverrideHistory(c.Request.Context(), tenantID, key)
	if err != nil {
		HandleError(c, err)
		return
	}
	resp := make([]OverrideHistoryResponse, len(hist))
	for i, h := range hist {
		resp[i] = overrideHistoryToResponse(h)
	}
	c.JSON(http.StatusOK, resp)
}

// actorFromClaims extracts the actor UUID from the gincommon RequestContext.
// Returns uuid.Nil for internal service routes where no authenticated user is present.
func actorFromClaims(c *gin.Context) uuid.UUID {
	rc, ok := gincommon.RequestContext(c)
	if !ok {
		return uuid.Nil
	}
	id, _ := uuid.Parse(rc.UserID)
	return id
}
