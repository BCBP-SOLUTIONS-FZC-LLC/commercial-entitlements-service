package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"

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
func (h *ShortfallHandler) List(c *gin.Context) {
	tenantID := tenantIDFromContext(c)

	var statusFilter []domain.ShortfallStatus
	if s := c.Query("status"); s != "" {
		statusFilter = []domain.ShortfallStatus{domain.ShortfallStatus(s)}
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

	sf, err := h.addonSvc.ResolveShortfall(c.Request.Context(), port.ResolveShortfallRequest{
		ShortfallID:    shortfallID,
		Disposition:    domain.ShortfallDisposition(body.Disposition),
		DispositionBy:  actorID,
		DispositionRef: body.DispositionRef,
		Note:           body.Note,
	})
	if err != nil {
		HandleError(c, err)
		return
	}
	c.JSON(http.StatusOK, shortfallToResponse(sf))
}
