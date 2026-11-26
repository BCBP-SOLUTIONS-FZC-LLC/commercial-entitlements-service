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
//
//	@Summary	Get plan definition for the tenant's current plan (internal)
//	@Tags		internal
//	@Produce	json
//	@Param		id			path		string	true	"Tenant UUID"
//	@Param		plan_code	query		string	true	"Plan code"
//	@Success	200			{object}	PlanResponse
//	@Failure	400			{object}	map[string]interface{}
//	@Failure	404			{object}	map[string]interface{}
//	@Router		/api/v1/internal/tenants/{id}/plans/current [get]
func (h *PlansHandler) GetCurrentPlan(c *gin.Context) {
	tenantID := tenantIDFromContext(c)
	planCode := c.Query("plan_code")
	if planCode == "" {
		gincommon.AbortWithProblem(c, problem.NewBadRequestError("plan_code query parameter is required"))
		return
	}
	_ = tenantID

	plan, err := h.catalogSvc.GetPlan(c.Request.Context(), planCode)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, planToResponse(plan))
}

// ListPlans handles GET /api/v1/operator/plans — lists all plan codes cached locally.
//
//	@Summary	List all cached plan codes
//	@Tags		plans
//	@Produce	json
//	@Success	200	{object}	map[string][]string
//	@Router		/api/v1/operator/plans [get]
func (h *PlansHandler) ListPlans(c *gin.Context) {
	codes, err := h.catalogSvc.ListPlans(c.Request.Context())
	if err != nil {
		HandleError(c, err)
		return
	}
	if codes == nil {
		codes = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"plan_codes": codes})
}

// RefreshCatalog handles POST /api/v1/operator/plans/:planCode/refresh —
// re-fetches the plan from IAM and warms the local cache.
//
//	@Summary	Refresh a plan definition from IAM and warm local cache
//	@Tags		plans
//	@Produce	json
//	@Param		planCode	path		string	true	"Plan code"
//	@Success	200			{object}	PlanResponse
//	@Failure	400			{object}	map[string]interface{}
//	@Failure	404			{object}	map[string]interface{}
//	@Router		/api/v1/operator/plans/{planCode}/refresh [post]
func (h *PlansHandler) RefreshCatalog(c *gin.Context) {
	planCode := c.Param("planCode")
	if planCode == "" {
		gincommon.AbortWithProblem(c, problem.NewBadRequestError("planCode path parameter is required"))
		return
	}
	plan, err := h.catalogSvc.RefreshPlan(c.Request.Context(), planCode)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, planToResponse(plan))
}
