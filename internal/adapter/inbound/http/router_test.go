package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestRouter assembles a minimal Router suitable for endpoint smoke tests.
func buildTestRouter(docs DocsConfig) *Router {
	tenantID := uuid.New()
	cfg := RouterConfig{
		GinConfig:          gincommon.Config{ServiceName: "test", BuildVersion: "test", Domain: "iam", Environment: "test"},
		Docs:               docs,
		EntitlementHandler: NewEntitlementHandler(&fakeEffectiveSvc{}),
		OverrideHandler:    NewOverrideHandler(&fakeOverrideSvc{}),
		AddonHandler:       NewAddonHandler(&fakeAddonSvc{}),
		ShortfallHandler:   NewShortfallHandler(&fakeAddonSvc{}),
		TrialHandler:       NewTrialHandler(&fakeTrialSvc{}),
		PlansHandler:       NewPlansHandler(&fakeCatalogSvc{}),
		Postgres:           &fakePinger{},
		Cache:              &fakePinger{},
		Outbox:             &fakePinger{},
		Liveness:           []Pinger{},
		Consumers:          []Pinger{},
	}
	_ = tenantID
	return NewRouter(cfg)
}

// ── Health endpoints ──────────────────────────────────────────────────────

func TestRouter_Healthz_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_Readyz_AllHealthy_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_Readyz_UnhealthyDep_503(t *testing.T) {
	cfg := RouterConfig{
		GinConfig:          gincommon.Config{ServiceName: "test", BuildVersion: "test", Domain: "iam", Environment: "test"},
		EntitlementHandler: NewEntitlementHandler(&fakeEffectiveSvc{}),
		OverrideHandler:    NewOverrideHandler(&fakeOverrideSvc{}),
		AddonHandler:       NewAddonHandler(&fakeAddonSvc{}),
		ShortfallHandler:   NewShortfallHandler(&fakeAddonSvc{}),
		TrialHandler:       NewTrialHandler(&fakeTrialSvc{}),
		PlansHandler:       NewPlansHandler(&fakeCatalogSvc{}),
		Postgres:           &fakePinger{err: errReadyz},
	}
	r := NewRouter(cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRouter_Readyz_UnhealthyConsumer_503(t *testing.T) {
	cfg := RouterConfig{
		GinConfig:          gincommon.Config{ServiceName: "test", BuildVersion: "test", Domain: "iam", Environment: "test"},
		EntitlementHandler: NewEntitlementHandler(&fakeEffectiveSvc{}),
		OverrideHandler:    NewOverrideHandler(&fakeOverrideSvc{}),
		AddonHandler:       NewAddonHandler(&fakeAddonSvc{}),
		ShortfallHandler:   NewShortfallHandler(&fakeAddonSvc{}),
		TrialHandler:       NewTrialHandler(&fakeTrialSvc{}),
		PlansHandler:       NewPlansHandler(&fakeCatalogSvc{}),
		Consumers:          []Pinger{&fakePinger{err: errReadyz}},
	}
	r := NewRouter(cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRouter_Livez_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_Livez_Unhealthy_503(t *testing.T) {
	cfg := RouterConfig{
		GinConfig:          gincommon.Config{ServiceName: "test", BuildVersion: "test", Domain: "iam", Environment: "test"},
		EntitlementHandler: NewEntitlementHandler(&fakeEffectiveSvc{}),
		OverrideHandler:    NewOverrideHandler(&fakeOverrideSvc{}),
		AddonHandler:       NewAddonHandler(&fakeAddonSvc{}),
		ShortfallHandler:   NewShortfallHandler(&fakeAddonSvc{}),
		TrialHandler:       NewTrialHandler(&fakeTrialSvc{}),
		PlansHandler:       NewPlansHandler(&fakeCatalogSvc{}),
		Liveness:           []Pinger{&fakePinger{err: errReadyz}},
	}
	r := NewRouter(cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── AsyncAPI spec endpoint ────────────────────────────────────────────────

func TestRouter_AsyncAPIYaml_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/asyncapi.yaml", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Docs endpoint (when enabled) ─────────────────────────────────────────

func TestRouter_AsyncAPIUI_DocsEnabled_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: true})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/asyncapi", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_AsyncAPIUI_WithAuthToken_Missing_401(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: true, AuthToken: "secret"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/asyncapi", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRouter_AsyncAPIUI_WithAuthToken_Valid_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: true, AuthToken: "secret"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/asyncapi", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Router.Handler() ──────────────────────────────────────────────────────

func TestRouter_Handler_ReturnsNonNil(t *testing.T) {
	r := buildTestRouter(DocsConfig{})
	require.NotNil(t, r.Handler())
}

type routerTestErr struct{}

func (routerTestErr) Error() string { return "unhealthy" }

var errReadyz error = routerTestErr{}
