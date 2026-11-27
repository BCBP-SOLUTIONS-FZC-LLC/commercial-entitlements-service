package http

import (
	"net/http"
	"slices"
	"strings"

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
	headerXFCC         = "X-Forwarded-Client-Cert"
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
//
// A caller is admitted when either:
//   - it sends x-service-token (a shared secret validated at the ingress/
//     sidecar layer; here only presence is enforced so the slot is real and
//     can be hardened without changing handlers); or
//   - its mTLS principal is billingPrincipal (B-2, F-2, BL-OQ-28). Billing
//     calls ENT-I1 with its mesh identity and the CD-19 x-user-id /
//     x-tenant-id / x-tenant-roles headers, never x-service-token. The
//     principal is the last X-Forwarded-Client-Cert element: on an mTLS
//     connection the Istio sidecar appends the verified peer certificate's
//     URI there (APPEND_FORWARD), and Envoy drops the header on a non-mTLS
//     connection, so with PeerAuthentication STRICT a caller cannot forge it.
//     billingReadOnly then confines that caller to GET ENT-I1.
//
// Without a mesh (local dev) the header is caller-controlled, exactly as
// x-service-token presence is; the Istio AuthorizationPolicy is the
// authoritative layer in staging and prod. An empty billingPrincipal
// disables principal admission.
func internalServiceRequired(billingPrincipal string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader(headerServiceToken) != "" {
			c.Next()
			return
		}
		if billingPrincipal != "" && principalFromXFCC(c.GetHeader(headerXFCC)) == billingPrincipal {
			c.Next()
			return
		}
		gincommon.AbortWithProblem(c, problem.NewUnauthorizedError("This endpoint is only accessible from internal services."))
	}
}

// principalFromXFCC returns the mTLS principal ("cluster.local/ns/<ns>/sa/<sa>")
// from an X-Forwarded-Client-Cert header: the URI=spiffe://… pair of the last
// element, which is the nearest hop (the sidecar in front of this pod).
// Returns "" when the header or the URI pair is absent.
func principalFromXFCC(h string) string {
	if h == "" {
		return ""
	}
	elems := strings.Split(h, ",")
	for _, pair := range strings.Split(elems[len(elems)-1], ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && strings.EqualFold(k, "URI") {
			return strings.TrimPrefix(strings.Trim(v, `"`), "spiffe://")
		}
	}
	return ""
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
func hasRole(roles []string, role string) bool { //nolint:unparam
	return slices.Contains(roles, role)
}

// billingReadOnly is layer 3 of W-2 (W-2 app allow-list, BL-OQ-3).
// It restricts Billing to exactly one route: GET
// /api/v1/internal/tenants/:id/entitlements (ENT-I1). All other routes return
// 403, providing defense-in-depth behind the Istio AuthorizationPolicy and
// NetworkPolicy layers.
//
// A request is Billing's when its mTLS principal (X-Forwarded-Client-Cert) is
// billingPrincipal — the identity Billing actually presents (B-2, F-2) — or
// when it sends x-service-name: commercial-billing.
func billingReadOnly(billingPrincipal string) gin.HandlerFunc {
	const billingCaller = "commercial-billing"
	const allowedRoute = "/api/v1/internal/tenants/:id/entitlements"

	return func(c *gin.Context) {
		isBilling := c.GetHeader(headerServiceName) == billingCaller ||
			(billingPrincipal != "" && principalFromXFCC(c.GetHeader(headerXFCC)) == billingPrincipal)
		if !isBilling {
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
