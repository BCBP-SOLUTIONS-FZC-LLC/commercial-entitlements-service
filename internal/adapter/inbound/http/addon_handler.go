package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// AddonHandler handles quota addon pack management and drawdowns (ENT-O5, ENT-I3).
type AddonHandler struct {
	addonSvc port.AddonService
}

// NewAddonHandler wires up the AddonHandler.
func NewAddonHandler(addonSvc port.AddonService) *AddonHandler {
	return &AddonHandler{addonSvc: addonSvc}
}

// List handles GET /api/v1/operator/tenants/:id/entitlements/addons (ENT-O5).
func (h *AddonHandler) List(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	addons, err := h.addonSvc.ListAddons(c.Request.Context(), tenantID)
	if err != nil {
		HandleError(c, err)
		return
	}
	resp := make([]AddonResponse, len(addons))
	for i := range addons {
		resp[i] = addonToResponse(&addons[i])
	}
	c.JSON(http.StatusOK, resp)
}

// Add handles POST /api/v1/operator/tenants/:id/entitlements/addons.
func (h *AddonHandler) Add(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	actorID := actorFromClaims(c)

	var body AddAddonBody
	if err := c.ShouldBindJSON(&body); err != nil {
		gincommon.AbortWithProblem(c, gincommon.BindingError(err))
		return
	}

	a, err := h.addonSvc.AddAddon(c.Request.Context(), port.AddAddonRequest{
		TenantID:    tenantID,
		LimitKey:    domain.LimitKey(body.LimitKey),
		TotalTokens: body.TotalTokens,
		GrantedBy:   actorID,
		Reason:      body.Reason,
		ExpiresAt:   body.ExpiresAt,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	metrics.IncAddonGrant(domain.LimitKey(body.LimitKey))
	c.JSON(http.StatusCreated, addonToResponse(a))
}

// Remove handles DELETE /api/v1/operator/tenants/:id/entitlements/addons/:addonId.
func (h *AddonHandler) Remove(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	actorID := actorFromClaims(c)

	addonID, err := uuid.Parse(c.Param("addonId"))
	if err != nil {
		gincommon.AbortWithProblem(c, problem.NewBadRequestError("addon ID must be a valid UUID"))
		return
	}

	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		gincommon.AbortWithProblem(c, gincommon.BindingError(err))
		return
	}

	if err := h.addonSvc.RemoveAddon(c.Request.Context(), port.RemoveAddonRequest{
		TenantID:  tenantID,
		AddonID:   addonID,
		RemovedBy: actorID,
		Reason:    body.Reason,
	}); err != nil {
		HandleError(c, err)
		return
	}
	metrics.IncAddonRemove()
	c.Status(http.StatusNoContent)
}

// ListDrawdowns handles GET /api/v1/operator/tenants/:id/entitlements/addons/drawdowns.
func (h *AddonHandler) ListDrawdowns(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	limitKey := domain.LimitKey(c.Query("limit_key"))
	if limitKey == "" {
		limitKey = domain.LimitKeyLLMTokenQuotaMonthly
	} else if !domain.KnownLimitKeys[limitKey] {
		gincommon.AbortWithProblem(c, problem.NewBadRequestError(
			"limit_key must be one of the recognised limit keys"))
		return
	}
	drawdowns, err := h.addonSvc.ListDrawdowns(c.Request.Context(), tenantID, limitKey)
	if err != nil {
		HandleError(c, err)
		return
	}
	resp := make([]DrawdownResponse, len(drawdowns))
	for i := range drawdowns {
		resp[i] = drawdownToResponse(&drawdowns[i])
	}
	c.JSON(http.StatusOK, resp)
}

// RecordDrawdownInternal handles ENT-I3: POST /api/v1/internal/tenants/:id/entitlements/drawdown
// Called by billing or usage services to deduct quota from addon packs.
func (h *AddonHandler) RecordDrawdownInternal(c *gin.Context) {
	tenantID := tenantIDFromContext(c)

	var body RecordDrawdownBody
	if err := c.ShouldBindJSON(&body); err != nil {
		gincommon.AbortWithProblem(c, gincommon.BindingError(err))
		return
	}

	var requestID *uuid.UUID
	if body.RequestID != nil {
		id, err := uuid.Parse(*body.RequestID)
		if err != nil {
			gincommon.AbortWithProblem(c, problem.NewBadRequestError("request_id must be a valid UUID"))
			return
		}
		requestID = &id
	}

	d, err := h.addonSvc.RecordDrawdown(c.Request.Context(), port.RecordDrawdownRequest{
		TenantID:       tenantID,
		LimitKey:       domain.LimitKey(body.LimitKey),
		TokensConsumed: body.TokensConsumed,
		PeriodStart:    body.PeriodStart,
		RequestID:      requestID,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, drawdownToResponse(d))
}
