// Package iamclient provides an HTTP client for the IAM plan catalog and
// tenant subscription endpoints. It uses otelhttp transport so outgoing calls
// are traced automatically (OBS-9).
package iamclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

// defaultTimeout is the per-request HTTP timeout for IAM catalog calls.
// Override with IAM_CATALOG_TIMEOUT (e.g. "5s", "15s") — LLD §12.
const defaultTimeout = 10 * time.Second

// Client is a plain net/http-based client for the IAM catalog service.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	log     port.Logger
}

// New returns a Client. baseURL and apiKey are read from env vars when empty.
// IAM_CATALOG_TIMEOUT overrides the default 10 s per-request HTTP deadline.
func New(log port.Logger) *Client {
	baseURL := os.Getenv("IAM_CATALOG_BASE_URL")
	apiKey := os.Getenv("IAM_CATALOG_API_KEY")
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout:   iamTimeout(),
			Transport: traceInjectTransport{http.DefaultTransport},
		},
		log: log,
	}
}

// NewWithConfig returns a Client with explicit baseURL and apiKey.
func NewWithConfig(baseURL, apiKey string, log port.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout:   iamTimeout(),
			Transport: traceInjectTransport{http.DefaultTransport},
		},
		log: log,
	}
}

// iamTimeout reads IAM_CATALOG_TIMEOUT; falls back to defaultTimeout.
func iamTimeout() time.Duration {
	if s := os.Getenv("IAM_CATALOG_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return defaultTimeout
}

// traceInjectTransport wraps every outbound IAM HTTP call in an OTel child
// span via gincommon.NewTracer (OBS-9) and propagates the W3C traceparent
// header so the IAM service can link to the caller's trace.
//
// This replaces otelhttp.NewTransport: gincommon is the platform's single
// entry point for OTel, so we do not import go.opentelemetry.io packages
// directly in internal adapter code.
type traceInjectTransport struct{ http.RoundTripper }

func (t traceInjectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	spanName := req.Method + " " + req.URL.Path
	ctx, span := gincommon.NewTracer("iamclient").Start(req.Context(), spanName)
	defer span.End()

	// Inject W3C traceparent so IAM can link its spans to ours.
	gincommon.InjectTraceContext(ctx, req.Header)

	resp, err := t.RoundTripper.RoundTrip(req.WithContext(ctx))
	if err != nil {
		gincommon.RecordSpanError(ctx, err, "IAM request failed")
	}
	return resp, err
}

var _ port.IAMClient = (*Client)(nil)

// FetchPlan fetches a plan definition from the IAM plan catalog endpoint.
func (c *Client) FetchPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	url := fmt.Sprintf("%s/internal/v1/plans/%s", c.baseURL, planCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("iam: build request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("iam: fetch plan %q: %w", planCode, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, domain.ErrPlanNotFound
	case http.StatusOK:
		// fall through
	default:
		return nil, fmt.Errorf("iam: fetch plan %q: unexpected status %d", planCode, resp.StatusCode)
	}

	var out domain.Plan
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("iam: decode plan response: %w", err)
	}
	return &out, nil
}

// FetchTenantSubscription fetches a tenant's current subscription state from IAM.
func (c *Client) FetchTenantSubscription(ctx context.Context, tenantID uuid.UUID) (*port.TenantSubscription, error) {
	url := fmt.Sprintf("%s/internal/v1/tenants/%s/subscription", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("iam: build request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("iam: fetch subscription for %s: %w", tenantID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, domain.ErrTenantNotFound
	case http.StatusOK:
		// fall through
	default:
		return nil, fmt.Errorf("iam: fetch subscription for %s: unexpected status %d", tenantID, resp.StatusCode)
	}

	var out port.TenantSubscription
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("iam: decode subscription response: %w", err)
	}
	return &out, nil
}
