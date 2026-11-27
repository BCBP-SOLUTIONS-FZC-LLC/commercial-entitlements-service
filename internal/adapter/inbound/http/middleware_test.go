package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	gincommon "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

// ── hasRole ───────────────────────────────────────────────────────────────

func TestHasRole(t *testing.T) {
	assert.True(t, hasRole([]string{"viewer", RoleOperator}, RoleOperator))
	assert.False(t, hasRole([]string{"viewer"}, RoleOperator))
	assert.False(t, hasRole(nil, RoleOperator))
}

// ── tenantIDGate ──────────────────────────────────────────────────────────

func TestTenantIDGate_ValidUUID_SetsContext(t *testing.T) {
	tenantID := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: tenantID.String()}}

	called := false
	tenantIDGate()(c)
	c.Next()
	_ = called

	got, exists := c.Get(contextKeyTenantID)
	assert.True(t, exists)
	assert.Equal(t, tenantID, got.(uuid.UUID))
}

func TestTenantIDGate_InvalidUUID_BadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}

	tenantIDGate()(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── tenantIDFromContext ────────────────────────────────────────────────────

func TestTenantIDFromContext_Missing_ReturnsNil(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	id := tenantIDFromContext(c)
	assert.Equal(t, uuid.Nil, id)
}

// ── operatorRequired ─────────────────────────────────────────────────────

func TestOperatorRequired_WithRole_Passes(t *testing.T) {
	r := gin.New()
	r.Use(operatorRequired())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	rc := &gincommon.RequestContextData{Roles: []string{RoleOperator}}
	c.Set(gincommon.RequestContextKey, rc)

	operatorRequired()(c)
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}

func TestOperatorRequired_MissingRC_Forbidden(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestOperatorRequired_WrongRole_Forbidden(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	rc := &gincommon.RequestContextData{Roles: []string{"viewer"}}
	c.Set(gincommon.RequestContextKey, rc)
	operatorRequired()(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ── internalServiceRequired ───────────────────────────────────────────────

func TestInternalServiceRequired_WithToken_Passes(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(headerServiceToken, "secret-token")
	c.Request = req
	internalServiceRequired("")(c)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)
}

func TestInternalServiceRequired_NoToken_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	internalServiceRequired("")(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── billingReadOnly ───────────────────────────────────────────────────────

func TestBillingReadOnly_NonBillingCaller_Passes(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/tenants/abc/something", nil)
	req.Header.Set(headerServiceName, "some-other-service")
	c.Request = req
	billingReadOnly("")(c)
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}

func TestBillingReadOnly_BillingOnAllowedRoute_Passes(t *testing.T) {
	r := gin.New()
	allowedPath := "/api/v1/internal/tenants/:id/entitlements"
	r.Use(billingReadOnly(""))
	r.GET(allowedPath, func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/tenants/abc/entitlements", nil)
	req.Header.Set(headerServiceName, "commercial-billing")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBillingReadOnly_BillingOnOtherRoute_Forbidden(t *testing.T) {
	r := gin.New()
	r.Use(billingReadOnly(""))
	r.POST("/api/v1/operator/tenants/:id/something", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/tenants/abc/something", nil)
	req.Header.Set(headerServiceName, "commercial-billing")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}
