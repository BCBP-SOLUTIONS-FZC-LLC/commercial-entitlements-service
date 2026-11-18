// Package http — error handling is fully delegated to platform-gincommon's
// RFC 9457 implementation. All error responses are written via
// gincommon.AbortWithProblem; the DomainError → HTTP status mapping is
// encapsulated in HandleError.
package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// HandleError maps a service error to an HTTP response using gincommon's
// RFC 9457 problem detail format and aborts the handler chain.
func HandleError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	var de *domain.DomainError
	if errors.As(err, &de) {
		prob := domainErrorToProblem(de)
		gincommon.AbortWithProblem(c, prob)
		return
	}
	gincommon.AbortWithProblem(c, problem.NewInternalError(err))
}

// domainErrorToProblem converts a DomainError to a *problem.Error.
func domainErrorToProblem(de *domain.DomainError) *problem.Error {
	status := statusForCode(de.Code)
	switch status {
	case http.StatusNotFound:
		return problem.NewNotFoundError(de.Message)
	case http.StatusForbidden:
		return problem.NewForbiddenError(de.Message)
	case http.StatusBadRequest:
		return problem.NewBadRequestError(de.Message)
	case http.StatusConflict:
		return problem.NewConflictError(de.Message)
	case http.StatusServiceUnavailable:
		return problem.NewServiceUnavailableError(de.Message)
	default:
		return problem.NewInternalError(de)
	}
}

// statusForCode maps domain error codes to HTTP status codes.
func statusForCode(code string) int {
	switch code {
	case domain.ErrTenantNotFound.Error(),
		domain.ErrOverrideNotFound.Error(),
		domain.ErrAddonNotFound.Error(),
		domain.ErrPlanNotFound.Error(),
		domain.ErrSnapshotNotFound.Error(),
		domain.ErrTrialAssignmentNotFound.Error(),
		domain.ErrTrialOverrideNotFound.Error(),
		domain.ErrShortfallNotFound.Error():
		return http.StatusNotFound

	case domain.ErrForbidden.Error(),
		domain.ErrOperatorRoleRequired.Error():
		return http.StatusForbidden

	case domain.ErrInvalidLimitKey.Error(),
		domain.ErrInvalidValue.Error(),
		domain.ErrReasonRequired.Error(),
		domain.ErrExpiresAtInPast.Error(),
		domain.ErrTrialEndsAtInPast.Error(),
		domain.ErrInvalidProfile.Error(),
		domain.ErrInvalidAction.Error(),
		domain.ErrTokensRequired.Error(),
		domain.ErrInvalidPeriodStart.Error(),
		domain.ErrDispositionRefRequired.Error(),
		domain.ErrRPSCannotBeUnlimited.Error():
		return http.StatusBadRequest

	case domain.ErrShortfallAlreadyResolved.Error(),
		domain.ErrTrialAlreadyActive.Error(),
		domain.ErrPackPartiallyUsed.Error(),
		domain.ErrDrawdownAlreadyRecorded.Error(),
		domain.ErrOptimisticLockConflict.Error(),
		domain.ErrConcurrentUpdate.Error():
		return http.StatusConflict

	case domain.ErrRequestTimeout.Error():
		return http.StatusServiceUnavailable

	case domain.ErrDBUnavailable.Error(),
		domain.ErrDependencyUnavailable.Error(),
		domain.ErrUpstreamUnavailable.Error(),
		domain.ErrCatalogUnavailable.Error():
		return http.StatusServiceUnavailable

	default:
		return http.StatusInternalServerError
	}
}
