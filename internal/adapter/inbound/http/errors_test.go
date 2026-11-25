package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

func TestHandleError_Nil_NoOp(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	HandleError(c, nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleError_DomainError_MapsStatus(t *testing.T) {
	cases := []struct {
		sentinel error
		wantCode int
	}{
		{domain.ErrOverrideNotFound, http.StatusNotFound},
		{domain.ErrAddonNotFound, http.StatusNotFound},
		{domain.ErrPlanNotFound, http.StatusNotFound},
		{domain.ErrSnapshotNotFound, http.StatusNotFound},
		{domain.ErrTrialAssignmentNotFound, http.StatusNotFound},
		{domain.ErrTrialOverrideNotFound, http.StatusNotFound},
		{domain.ErrShortfallNotFound, http.StatusNotFound},
		{domain.ErrTenantNotFound, http.StatusNotFound},

		{domain.ErrForbidden, http.StatusForbidden},
		{domain.ErrOperatorRoleRequired, http.StatusForbidden},

		{domain.ErrInvalidLimitKey, http.StatusBadRequest},
		{domain.ErrInvalidValue, http.StatusBadRequest},
		{domain.ErrReasonRequired, http.StatusBadRequest},
		{domain.ErrExpiresAtInPast, http.StatusBadRequest},
		{domain.ErrTrialEndsAtInPast, http.StatusBadRequest},
		{domain.ErrInvalidProfile, http.StatusBadRequest},
		{domain.ErrTokensRequired, http.StatusBadRequest},
		{domain.ErrInvalidPeriodStart, http.StatusBadRequest},
		{domain.ErrDispositionRefRequired, http.StatusBadRequest},
		{domain.ErrRPSCannotBeUnlimited, http.StatusBadRequest},
		{domain.ErrInvalidAction, http.StatusBadRequest},

		{domain.ErrShortfallAlreadyResolved, http.StatusConflict},
		{domain.ErrPackPartiallyUsed, http.StatusConflict},
		{domain.ErrOptimisticLockConflict, http.StatusConflict},

		{domain.ErrRequestTimeout, http.StatusServiceUnavailable},
		{domain.ErrDBUnavailable, http.StatusServiceUnavailable},
		{domain.ErrDependencyUnavailable, http.StatusServiceUnavailable},
		{domain.ErrCatalogUnavailable, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		HandleError(c, domain.NewError(tc.sentinel, "msg"))
		assert.Equal(t, tc.wantCode, w.Code, "sentinel %v", tc.sentinel)
	}
}

func TestHandleError_UnknownDomainCode_InternalServerError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	de := &domain.DomainError{Code: "some_unknown_code", Message: "unexpected"}
	HandleError(c, de)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleError_NonDomainError_InternalServerError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	HandleError(c, errors.New("raw infrastructure error"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
