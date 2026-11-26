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

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewWithConfig(srv.URL, "test-key", nopIAMLogger{})
}

// ── FetchPlan ─────────────────────────────────────────────────────────────

func TestFetchPlan_200_DecodesResponse(t *testing.T) {
	plan := domain.Plan{
		Code:        "starter",
		DisplayName: "Starter Plan",
		ModelTier:   "standard",
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/plans/starter", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
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

func TestFetchPlan_NoAPIKey_OmitsHeader(t *testing.T) {
	c := NewWithConfig("", "", nopIAMLogger{}) // empty key
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(domain.Plan{Code: "starter"})
	}))
	c.baseURL = srv.URL
	defer srv.Close()
	_, err := c.FetchPlan(context.Background(), "starter")
	require.NoError(t, err)
}

// ── FetchTenantSubscription ───────────────────────────────────────────────

func TestFetchTenantSubscription_200_DecodesResponse(t *testing.T) {
	tenantID := uuid.New()
	sub := port.TenantSubscription{
		TenantID:      tenantID,
		PlanCode:      "pro",
		State:         "active",
		RecordVersion: 1,
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, tenantID.String())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sub)
	}))
	got, err := c.FetchTenantSubscription(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.PlanCode)
	assert.Equal(t, "active", got.State)
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
	t.Setenv("IAM_CATALOG_BASE_URL", "https://iam.example.com")
	t.Setenv("IAM_CATALOG_API_KEY", "my-key")
	c := New(nopIAMLogger{})
	assert.Equal(t, "https://iam.example.com", c.baseURL)
	assert.Equal(t, "my-key", c.apiKey)
}

func TestNewWithConfig_SetsFields(t *testing.T) {
	c := NewWithConfig("https://iam.test", "api-key", nopIAMLogger{})
	assert.Equal(t, "https://iam.test", c.baseURL)
	assert.Equal(t, "api-key", c.apiKey)
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
	c := NewWithConfig("http://127.0.0.1:1", "", nopIAMLogger{}) // port 1 is always refused
	_, err := c.FetchPlan(context.Background(), "some_plan")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch plan")
}

// ── FetchTenantSubscription — http.Do transport error ───────────────────

func TestFetchTenantSubscription_TransportError_ReturnsError(t *testing.T) {
	c := NewWithConfig("http://127.0.0.1:1", "", nopIAMLogger{})
	_, err := c.FetchTenantSubscription(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch subscription")
}
