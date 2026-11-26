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

// ShortfallHandler handles quota shortfall listing and resolution (ENT-O6, ENT-O7).
type ShortfallHandler struct {
	addonSvc port.AddonService
}

// NewShortfallHandler wires up the ShortfallHandler.
func NewShortfallHandler(addonSvc port.AddonService) *ShortfallHandler {
	return &ShortfallHandler{addonSvc: addonSvc}
}

// List handles GET /api/v1/operator/tenants/:id/entitlements/addons/shortfalls (ENT-O6).
//
//	@Summary	List quota shortfalls
//	@Tags		shortfalls
//	@Produce	json
//	@Param		id		path		string	true	"Tenant UUID"
//	@Param		status	query		string	false	"Filter by status (pending_approval, auto_accepted, approved, rejected, expired)"
//	@Success	200		{array}		ShortfallResponse
//	@Failure	400		{object}	map[string]interface{}
//	@Router		/api/v1/operator/tenants/{id}/entitlements/addons/shortfalls [get]
func (h *ShortfallHandler) List(c *gin.Context) {
	tenantID := tenantIDFromContext(c)

	var statusFilter []domain.ShortfallStatus
	if s := c.Query("status"); s != "" {
		st := domain.ShortfallStatus(s)
		if _, ok := domain.ValidShortfallStatuses[st]; !ok {
			gincommon.AbortWithProblem(c, problem.NewBadRequestError(
				"status must be one of: pending, auto_accepted, pending_approval, approved, rejected, expired"))
			return
		}
		statusFilter = []domain.ShortfallStatus{st}
	}

	shortfalls, err := h.addonSvc.ListShortfalls(c.Request.Context(), tenantID, statusFilter)
	if err != nil {
		HandleError(c, err)
		return
	}
	resp := make([]ShortfallResponse, len(shortfalls))
	for i := range shortfalls {
		resp[i] = shortfallToResponse(&shortfalls[i])
	}
	c.JSON(http.StatusOK, resp)
}

// Resolve handles POST /api/v1/operator/tenants/:id/entitlements/addons/shortfalls/:shortfallId/resolve (ENT-O7).
//
//	@Summary	Approve or reject a pending shortfall
//	@Tags		shortfalls
//	@Accept		json
//	@Produce	json
//	@Param		id				path		string					true	"Tenant UUID"
//	@Param		shortfallId		path		string					true	"Shortfall UUID"
//	@Param		body			body		ResolveShortfallBody	true	"Resolution (approve or reject)"
//	@Success	200				{object}	ShortfallResponse
//	@Failure	400				{object}	map[string]interface{}
//	@Failure	404				{object}	map[string]interface{}
//	@Router		/api/v1/operator/tenants/{id}/entitlements/addons/shortfalls/{shortfallId}/resolve [post]
func (h *ShortfallHandler) Resolve(c *gin.Context) {
	actorID := actorFromClaims(c)

	shortfallID, err := uuid.Parse(c.Param("shortfallId"))
	if err != nil {
		gincommon.AbortWithProblem(c, problem.NewBadRequestError("shortfall ID must be a valid UUID"))
		return
	}

	var body ResolveShortfallBody
	if err := c.ShouldBindJSON(&body); err != nil {
		gincommon.AbortWithProblem(c, gincommon.BindingError(err))
		return
	}

	disp := domain.ShortfallDisposition(body.Disposition)
	if _, ok := domain.ValidShortfallDispositions[disp]; !ok {
		gincommon.AbortWithProblem(c, problem.NewBadRequestError(
			"disposition must be one of: approve, reject"))
		return
	}

	sf, err := h.addonSvc.ResolveShortfall(c.Request.Context(), port.ResolveShortfallRequest{
		ShortfallID:    shortfallID,
		Disposition:    disp,
		DispositionBy:  actorID,
		DispositionRef: body.DispositionRef,
		Note:           body.Note,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	metrics.IncShortfallResolve(disp)
	c.JSON(http.StatusOK, shortfallToResponse(sf))
}
