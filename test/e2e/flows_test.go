//go:build e2e

// Phase 7 · P7-FLOW — end-to-end HTTP flows via the running server.
//
//   - P7-FLOW-001: GET /healthz returns 200.
//   - P7-FLOW-002: GET /readyz returns 200.
//   - P7-FLOW-003: internal GET entitlements returns ErrSnapshotNotFound (404)
//     for an unknown tenant.
//   - P7-FLOW-004: internal GET entitlements returns 200 when a snapshot
//     exists.
//   - P7-FLOW-005: internal POST recompute returns 200.
//   - P7-FLOW-006: operator PUT override then GET override round-trip.
package e2e_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test Case ID:      P7-FLOW-001
// Feature:           Health check
// Expected Result:   GET /healthz → 200
func TestP7Flow001_Healthz(t *testing.T) {
	e := newE2EEnv(t)
	code, _, _ := e.do(t, reqOpts{method: http.MethodGet, path: "/healthz"})
	assert.Equal(t, http.StatusOK, code)
}

// Test Case ID:      P7-FLOW-002
// Feature:           Readiness check
// Expected Result:   GET /readyz → 200
func TestP7Flow002_Readyz(t *testing.T) {
	e := newE2EEnv(t)
	code, _, _ := e.do(t, reqOpts{method: http.MethodGet, path: "/readyz"})
	assert.Equal(t, http.StatusOK, code)
}

// Test Case ID:      P7-FLOW-003
// Feature:           Internal GET entitlements — unknown tenant
// Scenario:          No snapshot seeded → service returns ErrSnapshotNotFound
// Expected Result:   404 Not Found
func TestP7Flow003_GetEntitlementsInternalNotFound(t *testing.T) {
	e := newE2EEnv(t)
	unknown := uuid.New()
	code, _, _ := e.do(t, reqOpts{
		method:  http.MethodGet,
		path:    tenantPath(unknown, "/entitlements"),
		headers: serviceHeaders(),
	})
	assert.Equal(t, http.StatusNotFound, code,
		"P7-FLOW-003: unknown tenant must return 404")
}

// Test Case ID:      P7-FLOW-004
// Feature:           Internal GET entitlements — happy path
// Scenario:          Snapshot seeded → 200 with effective set
// Expected Result:   200, body contains limits map
func TestP7Flow004_GetEntitlementsInternalHappy(t *testing.T) {
	e := newE2EEnv(t)
	tenantID := e.tenantID
	e.seedSnapshot(t, tenantID, "starter")

	code, _, body := e.do(t, reqOpts{
		method:  http.MethodGet,
		path:    tenantPath(tenantID, "/entitlements"),
		headers: serviceHeaders(),
	})
	require.Equal(t, http.StatusOK, code,
		"P7-FLOW-004: seeded tenant must return 200 (body=%s)", string(body))

	var resp map[string]any
	unmarshalBody(t, body, &resp)
	assert.Equal(t, tenantID.String(), resp["tenant_id"],
		"P7-FLOW-004: tenant_id must match")
}

// Test Case ID:      P7-FLOW-005
// Feature:           Internal POST recompute
// Scenario:          Recompute triggers snapshot computation via IAM client
// Expected Result:   200 with fresh effective set
func TestP7Flow005_RecomputeInternal(t *testing.T) {
	e := newE2EEnv(t)
	tenantID := e.tenantID

	code, _, body := e.do(t, reqOpts{
		method:  http.MethodPost,
		path:    tenantPath(tenantID, "/entitlements/recompute"),
		headers: serviceHeaders(),
	})
	require.Equal(t, http.StatusOK, code,
		"P7-FLOW-005: recompute must return 200 (body=%s)", string(body))

	var resp map[string]any
	unmarshalBody(t, body, &resp)
	assert.Equal(t, tenantID.String(), resp["tenant_id"])
}

// Test Case ID:      P7-FLOW-006
// Feature:           Operator PUT override → GET override round-trip
// Scenario:          Set an override for a tenant via operator route, read it back
// Expected Result:   PUT 200, GET 200 with matching value
func TestP7Flow006_OverrideRoundTrip(t *testing.T) {
	e := newE2EEnv(t)
	tenantID := e.tenantID
	userID := uuid.New().String()

	opPath := func(suffix string) string {
		return "/api/v1/operator/tenants/" + tenantID.String() + suffix
	}

	// PUT the override.
	code, _, body := e.do(t, reqOpts{
		method:  http.MethodPut,
		path:    opPath("/entitlements/overrides/llm_token_quota_monthly"),
		headers: operatorHeaders(userID, tenantID),
		body: map[string]any{
			"value":  200_000,
			"reason": "e2e test override",
		},
	})
	require.Equal(t, http.StatusOK, code,
		"P7-FLOW-006: PUT override must return 200 (body=%s)", string(body))

	// GET the override back.
	code, _, body = e.do(t, reqOpts{
		method:  http.MethodGet,
		path:    opPath("/entitlements/overrides/llm_token_quota_monthly"),
		headers: operatorHeaders(userID, tenantID),
	})
	require.Equal(t, http.StatusOK, code,
		"P7-FLOW-006: GET override must return 200 (body=%s)", string(body))

	var got map[string]any
	unmarshalBody(t, body, &got)
	assert.Equal(t, "llm_token_quota_monthly", got["limit_key"],
		"P7-FLOW-006: limit_key must match")
}

// Test Case ID:      P7-FLOW-007
// Feature:           Missing service token on internal route
// Scenario:          Internal route called without x-service-token header
// Expected Result:   401 Unauthorized
func TestP7Flow007_InternalRouteRequiresServiceToken(t *testing.T) {
	e := newE2EEnv(t)
	code, _, _ := e.do(t, reqOpts{
		method:  http.MethodGet,
		path:    tenantPath(e.tenantID, "/entitlements"),
		headers: map[string]string{}, // no service token
	})
	assert.Equal(t, http.StatusUnauthorized, code,
		"P7-FLOW-007: missing service token must return 401")
}
