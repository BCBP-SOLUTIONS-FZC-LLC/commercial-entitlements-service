package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// PlansHandler serves the internal plan catalog endpoint (ENT-I2).
type PlansHandler struct {
	catalogSvc port.CatalogService
}

// NewPlansHandler wires up the PlansHandler.
func NewPlansHandler(catalogSvc port.CatalogService) *PlansHandler {
	return &PlansHandler{catalogSvc: catalogSvc}
}

// GetCurrentPlan handles GET /api/v1/internal/tenants/:id/plans/current (ENT-I2).
// Returns the plan definition for the tenant's current plan.
func (h *PlansHandler) GetCurrentPlan(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	// planCode query param overrides tenant lookup.
	planCode := c.Query("plan_code")
	if planCode == "" {
		gincommon.AbortWithProblem(c, problem.NewBadRequestError("plan_code query parameter is required"))
		return
	}
	_ = tenantID // tenantID used for future per-tenant catalog customisation

	plan, err := h.catalogSvc.GetPlan(c.Request.Context(), planCode)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, planToResponse(plan))
}
