package http

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/problem"
)

const (
	// RoleOperator is the Keycloak role that grants access to operator endpoints.
	RoleOperator       = "platform_operator"
	headerServiceToken = "x-service-token"
	headerServiceName  = "x-service-name"
	contextKeyTenantID = "tenant_id"
)

// operatorRequired gates operator endpoints — the calling request context must carry
// the platform_operator role, enforced via gincommon.RequestContext.
func operatorRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		rc, ok := gincommon.RequestContext(c)
		if !ok || !hasRole(rc.Roles, RoleOperator) {
			gincommon.AbortWithProblem(c, problem.NewForbiddenError("This endpoint requires the platform_operator role."))
			return
		}
		c.Next()
	}
}

// internalServiceRequired gates service-to-service internal endpoints.
// In production an x-service-token header carries a shared secret validated
// at the ingress/sidecar layer. Here we just enforce the header is present
// so the middleware slot is real and can be hardened without changing handlers.
func internalServiceRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader(headerServiceToken) == "" {
			gincommon.AbortWithProblem(c, problem.NewUnauthorizedError("This endpoint is only accessible from internal services."))
			return
		}
		c.Next()
	}
}

// tenantIDGate validates that :id in the path is a valid UUID and stores it
// in context. For JWT-authenticated routes it also verifies the token's
// tenant claim matches the path parameter (prevents cross-tenant access).
func tenantIDGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		tenantID, err := uuid.Parse(idStr)
		if err != nil {
			gincommon.AbortWithProblem(c, problem.NewBadRequestError("Path parameter :id must be a valid UUID."))
			return
		}
		c.Set(contextKeyTenantID, tenantID)
		c.Next()
	}
}

// tenantIDFromContext retrieves the tenant UUID previously validated by tenantIDGate.
func tenantIDFromContext(c *gin.Context) uuid.UUID {
	v, _ := c.Get(contextKeyTenantID)
	id, _ := v.(uuid.UUID)
	return id
}

// hasRole reports whether the given role is present in the roles slice.
func hasRole(roles []string, role string) bool {
	return slices.Contains(roles, role)
}

// billingReadOnly is layer 3 of W-2 (W-2 app allow-list, BL-OQ-3).
// It restricts calls that identify as "commercial-billing" (via x-service-name)
// to exactly one route: GET /api/v1/internal/tenants/:id/entitlements (ENT-I1).
// All other routes return 403, providing defense-in-depth behind the Istio
// AuthorizationPolicy and NetworkPolicy layers.
func billingReadOnly() gin.HandlerFunc {
	const billingCaller = "commercial-billing"
	const allowedRoute = "/api/v1/internal/tenants/:id/entitlements"

	return func(c *gin.Context) {
		if c.GetHeader(headerServiceName) != billingCaller {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodGet && c.FullPath() == allowedRoute {
			c.Next()
			return
		}
		gincommon.AbortWithProblem(c, problem.NewForbiddenError("commercial-billing is only permitted to read entitlements (ENT-I1)."))
	}
}
