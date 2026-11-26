package iamclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

type nopIAMLogger struct{}

func (nopIAMLogger) Info(_ string, _ map[string]any)  {}
func (nopIAMLogger) Warn(_ string, _ map[string]any)  {}
func (nopIAMLogger) Error(_ string, _ map[string]any) {}
func (nopIAMLogger) Debug(_ string, _ map[string]any) {}

// newTestClient points both catalogBaseURL and orgBaseURL at the same test server
// (each test only exercises one endpoint at a time).
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewWithConfig(srv.URL, srv.URL, nopIAMLogger{})
}

// ── FetchPlan ─────────────────────────────────────────────────────────────

func TestFetchPlan_200_DecodesResponse(t *testing.T) {
	plan := domain.Plan{
		Code:        "starter",
		DisplayName: "Starter Plan",
		ModelTier:   "standard",
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/internal/plans/starter", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plan)
	}))
	got, err := c.FetchPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
	assert.Equal(t, "standard", got.ModelTier)
}

func TestFetchPlan_404_ReturnsErrPlanNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c.FetchPlan(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrPlanNotFound)
}

func TestFetchPlan_500_ReturnsError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, err := c.FetchPlan(context.Background(), "starter")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestFetchPlan_InvalidJSON_ReturnsDecodeError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
	}))
	_, err := c.FetchPlan(context.Background(), "starter")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode plan response")
}

// ── FetchAllPlans ─────────────────────────────────────────────────────────

func TestFetchAllPlans_200_DecodesAll(t *testing.T) {
	plans := []domain.Plan{
		{Code: "starter", ModelTier: "basic"},
		{Code: "pro", ModelTier: "standard"},
		{Code: "enterprise", ModelTier: "premium"},
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/internal/plans", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"plans": plans})
	}))
	got, err := c.FetchAllPlans(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "starter", got[0].Code)
	assert.Equal(t, "enterprise", got[2].Code)
}

func TestFetchAllPlans_NonOK_ReturnsError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, err := c.FetchAllPlans(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestFetchAllPlans_InvalidJSON_ReturnsDecodeError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`bad`))
	}))
	_, err := c.FetchAllPlans(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode all plans response")
}

func TestFetchAllPlans_TransportError_ReturnsError(t *testing.T) {
	c := NewWithConfig("http://127.0.0.1:1", "http://127.0.0.1:1", nopIAMLogger{})
	_, err := c.FetchAllPlans(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch all plans")
}

// ── FetchTenantSubscription ───────────────────────────────────────────────

func TestFetchTenantSubscription_200_DecodesResponse(t *testing.T) {
	tenantID := uuid.New()
	// Match iam-org-membership I-17 plan-context wire shape exactly.
	resp := map[string]any{
		"tenant_id":      tenantID,
		"plan":           "pro",
		"status":         "active",
		"record_version": 1,
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("/api/v1/internal/tenants/%s/plan-context", tenantID), r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	got, err := c.FetchTenantSubscription(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.PlanCode)
	assert.Equal(t, "active", got.State)
}

func TestFetchTenantSubscription_WithTrialEndsAt_DecodesOptionalFields(t *testing.T) {
	tenantID := uuid.New()
	trialEnd := "2027-04-01T00:00:00Z"
	resp := map[string]any{
		"tenant_id":      tenantID,
		"plan":           "starter",
		"status":         "trial",
		"trial_ends_at":  trialEnd,
		"record_version": 2,
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	got, err := c.FetchTenantSubscription(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "starter", got.PlanCode)
	assert.Equal(t, "trial", got.State)
	require.NotNil(t, got.TrialEndsAt)
}

func TestFetchTenantSubscription_404_ReturnsErrTenantNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c.FetchTenantSubscription(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTenantNotFound)
}

func TestFetchTenantSubscription_500_ReturnsError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	_, err := c.FetchTenantSubscription(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status")
}

func TestFetchTenantSubscription_InvalidJSON_ReturnsDecodeError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`bad`))
	}))
	_, err := c.FetchTenantSubscription(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode subscription response")
}

// ── New / NewWithConfig ───────────────────────────────────────────────────

func TestNew_ReadsEnvVars(t *testing.T) {
	t.Setenv("IAM_CATALOG_BASE_URL", "https://catalog.example.com")
	t.Setenv("IAM_ORG_BASE_URL", "https://org.example.com")
	c := New(nopIAMLogger{})
	assert.Equal(t, "https://catalog.example.com", c.catalogBaseURL)
	assert.Equal(t, "https://org.example.com", c.orgBaseURL)
}

func TestNewWithConfig_SetsFields(t *testing.T) {
	c := NewWithConfig("https://catalog.test", "https://org.test", nopIAMLogger{})
	assert.Equal(t, "https://catalog.test", c.catalogBaseURL)
	assert.Equal(t, "https://org.test", c.orgBaseURL)
}

// ── iamTimeout ───────────────────────────────────────────────────────────────

func TestIAMTimeout_Default_WhenEnvUnset(t *testing.T) {
	t.Setenv("IAM_CATALOG_TIMEOUT", "")
	got := iamTimeout()
	assert.Equal(t, defaultTimeout, got, "unset env → default 10s")
}

func TestIAMTimeout_CustomValue_WhenEnvSet(t *testing.T) {
	t.Setenv("IAM_CATALOG_TIMEOUT", "5s")
	got := iamTimeout()
	assert.Equal(t, 5*time.Second, got, "valid env value must be used")
}

func TestIAMTimeout_FallbackOnInvalid_WhenEnvBad(t *testing.T) {
	t.Setenv("IAM_CATALOG_TIMEOUT", "not-a-duration")
	got := iamTimeout()
	assert.Equal(t, defaultTimeout, got, "unparseable env → default")
}

// ── traceInjectTransport.RoundTrip error path ─────────────────────────────

type errorTransport struct{ err error }

func (e *errorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, e.err
}

func TestRoundTrip_TransportError_RecordsSpanError(t *testing.T) {
	inner := &errorTransport{err: fmt.Errorf("connection refused")}
	tr := traceInjectTransport{RoundTripper: inner}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost/test", nil)
	resp, err := tr.RoundTrip(req) //nolint:bodyclose // resp is nil on transport error; nothing to close
	require.Error(t, err)
	assert.Nil(t, resp, "response must be nil on transport error")
	assert.Contains(t, err.Error(), "connection refused")
}

// ── FetchPlan — http.Do transport error ──────────────────────────────────

func TestFetchPlan_TransportError_ReturnsError(t *testing.T) {
	c := NewWithConfig("http://127.0.0.1:1", "http://127.0.0.1:1", nopIAMLogger{})
	_, err := c.FetchPlan(context.Background(), "some_plan")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch plan")
}

// ── FetchTenantSubscription — http.Do transport error ───────────────────

func TestFetchTenantSubscription_TransportError_ReturnsError(t *testing.T) {
	c := NewWithConfig("http://127.0.0.1:1", "http://127.0.0.1:1", nopIAMLogger{})
	_, err := c.FetchTenantSubscription(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch subscription")
}

// Keep port.TenantSubscription in scope so the import is used.
var _ = port.TenantSubscription{}
