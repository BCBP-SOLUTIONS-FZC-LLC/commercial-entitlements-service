package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// B-2 (F-1, F-2, BL-OQ-28): Billing calls ENT-I1 with its mesh identity and
// the CD-19 headers, never x-service-token / x-service-name.

const testBillingPrincipal = "cluster.local/ns/billing/sa/commercial-billing"

// billingXFCC is the header Istio's sidecar forwards for a Billing request:
// the verified peer is the last element.
const billingXFCC = `By=spiffe://cluster.local/ns/commercial/sa/commercial-entitlements;` +
	`Hash=abc123;Subject="";URI=spiffe://cluster.local/ns/billing/sa/commercial-billing`

func billingRouter() *Router {
	return NewRouter(RouterConfig{
		GinConfig:          gincommon.Config{ServiceName: "test", BuildVersion: "test", Domain: "iam", Environment: "test"},
		EntitlementHandler: NewEntitlementHandler(&fakeEffectiveSvc{}),
		OverrideHandler:    NewOverrideHandler(&fakeOverrideSvc{}),
		AddonHandler:       NewAddonHandler(&fakeAddonSvc{}),
		ShortfallHandler:   NewShortfallHandler(&fakeAddonSvc{}),
		TrialHandler:       NewTrialHandler(&fakeTrialSvc{}),
		PlansHandler:       NewPlansHandler(&fakeCatalogSvc{}),
		Postgres:           &fakePinger{},
		Cache:              &fakePinger{},
		Outbox:             &fakePinger{},
		BillingPrincipal:   testBillingPrincipal,
	})
}

// billingRequest builds a request exactly as Billing sends it
// (internal/adapters/httpx/call.go): CD-19 headers, no service token.
func billingRequest(method, path, xfcc string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req.Header.Set("x-user-id", uuid.NewString())
	req.Header.Set("x-tenant-id", uuid.NewString())
	req.Header.Set("x-tenant-roles", "tenant_admin")
	if xfcc != "" {
		req.Header.Set(headerXFCC, xfcc)
	}
	return req
}

func TestBillingPrincipal_ENTI1_200(t *testing.T) {
	w := httptest.NewRecorder()
	billingRouter().Handler().ServeHTTP(w, billingRequest(http.MethodGet,
		"/api/v1/internal/tenants/"+uuid.NewString()+"/entitlements", billingXFCC))
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestBillingPrincipal_OtherInternalRoutes_403(t *testing.T) {
	tenant := uuid.NewString()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/internal/tenants/" + tenant + "/entitlements/recompute"}, // ENT-I1b
		{http.MethodGet, "/api/v1/internal/tenants/" + tenant + "/plans/current"},           // ENT-I2
		{http.MethodPost, "/api/v1/internal/tenants/" + tenant + "/entitlements/drawdown"},  // ENT-I3
	} {
		w := httptest.NewRecorder()
		billingRouter().Handler().ServeHTTP(w, billingRequest(tc.method, tc.path, billingXFCC))
		assert.Equal(t, http.StatusForbidden, w.Code, "%s %s", tc.method, tc.path)
	}
}

func TestBillingPrincipal_Absent_Rejected(t *testing.T) {
	w := httptest.NewRecorder()
	billingRouter().Handler().ServeHTTP(w, billingRequest(http.MethodGet,
		"/api/v1/internal/tenants/"+uuid.NewString()+"/entitlements", ""))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBillingPrincipal_OtherPrincipal_Rejected(t *testing.T) {
	xfcc := `By=spiffe://cluster.local/ns/commercial/sa/commercial-entitlements;URI=spiffe://cluster.local/ns/other/sa/intruder`
	w := httptest.NewRecorder()
	billingRouter().Handler().ServeHTTP(w, billingRequest(http.MethodGet,
		"/api/v1/internal/tenants/"+uuid.NewString()+"/entitlements", xfcc))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// A forged Billing element followed by the sidecar's real one: only the last
// (verified) element counts.
func TestBillingPrincipal_ForgedEarlierElement_Ignored(t *testing.T) {
	xfcc := `URI=spiffe://cluster.local/ns/billing/sa/commercial-billing,` +
		`By=spiffe://cluster.local/ns/commercial/sa/commercial-entitlements;URI=spiffe://cluster.local/ns/other/sa/intruder`
	w := httptest.NewRecorder()
	billingRouter().Handler().ServeHTTP(w, billingRequest(http.MethodGet,
		"/api/v1/internal/tenants/"+uuid.NewString()+"/entitlements", xfcc))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBillingPrincipal_Disabled_NotAdmitted(t *testing.T) {
	r := buildTestRouter(DocsConfig{}) // BillingPrincipal empty
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, billingRequest(http.MethodGet,
		"/api/v1/internal/tenants/"+uuid.NewString()+"/entitlements", billingXFCC))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// x-service-token callers keep full internal access.
func TestServiceTokenCaller_InternalRoutes_Unaffected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/tenants/"+uuid.NewString()+"/plans/current", nil)
	req.Header.Set(headerServiceToken, "t")
	w := httptest.NewRecorder()
	billingRouter().Handler().ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}

func TestPrincipalFromXFCC(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"Hash=abc;Subject=\"\"":      "",
		billingXFCC:                  testBillingPrincipal,
		`uri="spiffe://a/ns/b/sa/c"`: "a/ns/b/sa/c",
		"URI=spiffe://x/ns/y/sa/z,By=b;URI=spiffe://cluster.local/ns/billing/sa/commercial-billing": testBillingPrincipal,
	} {
		assert.Equal(t, want, principalFromXFCC(in), in)
	}
}
