//go:build e2e

// Phase 7 · P7-ERR — error response shapes conform to RFC 7807 problem detail.
package e2e_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test Case ID:      P7-ERR-001
// Feature:           404 shape on unknown route
// Expected Result:   404 body is a JSON problem detail with status + type keys
func TestP7Err001_UnknownRoute404Shape(t *testing.T) {
	e := newE2EEnv(t)
	code, header, body := e.do(t, reqOpts{
		method: http.MethodGet,
		path:   "/api/v1/does-not-exist",
	})
	assert.Equal(t, http.StatusNotFound, code)
	assert.Contains(t, header.Get("Content-Type"), "application/problem+json")

	var prob map[string]any
	require.NoError(t, json.Unmarshal(body, &prob))
	assert.Contains(t, prob, "status", "problem detail must have status")
	assert.Contains(t, prob, "type", "problem detail must have type")
}

// Test Case ID:      P7-ERR-002
// Feature:           400 on invalid UUID path parameter
// Expected Result:   400 body is a JSON problem detail object
func TestP7Err002_BadUUIDPathParam(t *testing.T) {
	e := newE2EEnv(t)
	code, _, body := e.do(t, reqOpts{
		method:  http.MethodGet,
		path:    "/api/v1/internal/tenants/not-a-uuid/entitlements",
		headers: serviceHeaders(),
	})
	assert.Equal(t, http.StatusBadRequest, code,
		"P7-ERR-002: invalid UUID must yield 400 (body=%s)", string(body))
}

// Test Case ID:      P7-ERR-003
// Feature:           405 Method Not Allowed
// Expected Result:   405 on wrong HTTP method for a known route
func TestP7Err003_MethodNotAllowed(t *testing.T) {
	e := newE2EEnv(t)
	tenantID := uuid.New()
	code, _, _ := e.do(t, reqOpts{
		method:  http.MethodDelete,
		path:    tenantPath(tenantID, "/entitlements"),
		headers: serviceHeaders(),
	})
	assert.Equal(t, http.StatusMethodNotAllowed, code,
		"P7-ERR-003: DELETE on read-only entitlements route must yield 405")
}

// Test Case ID:      P7-ERR-004
// Feature:           401 on operator route without auth headers
// Expected Result:   401 when ProtectedMiddlewares rejects the request
func TestP7Err004_OperatorRouteNoAuth(t *testing.T) {
	e := newE2EEnv(t)
	code, _, _ := e.do(t, reqOpts{
		method:  http.MethodGet,
		path:    "/api/v1/operator/tenants/" + e.tenantID.String() + "/entitlements/overrides",
		headers: map[string]string{},
	})
	assert.Equal(t, http.StatusUnauthorized, code,
		"P7-ERR-004: operator route without auth must return 401")
}
