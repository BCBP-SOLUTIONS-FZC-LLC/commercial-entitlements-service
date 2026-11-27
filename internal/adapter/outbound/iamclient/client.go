// Package iamclient provides an HTTP client for two upstream IAM services:
//   - iam-catalog-admin  (IAM_CATALOG_BASE_URL) — plan definitions via CAT-I2
//   - iam-org-membership (IAM_ORG_BASE_URL)      — tenant subscription state via I-17
//
// Both services use Istio mTLS for in-mesh auth; no API key is required.
// Outgoing calls are wrapped in OTel child spans (OBS-9).
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

// defaultTimeout is the per-request HTTP timeout for IAM calls.
// Override with IAM_CATALOG_TIMEOUT (e.g. "5s", "15s") — LLD §12.
const defaultTimeout = 10 * time.Second

// Client is a plain net/http-based client for the IAM catalog and org-membership services.
type Client struct {
	catalogBaseURL string // iam-catalog-admin: plan definitions (CAT-I2)
	orgBaseURL     string // iam-org-membership: tenant subscription state (I-17)
	http           *http.Client
	log            port.Logger
}

// New returns a Client reading base URLs from env vars.
// IAM_CATALOG_BASE_URL → iam-catalog-admin; IAM_ORG_BASE_URL → iam-org-membership.
// IAM_CATALOG_TIMEOUT overrides the default 10 s per-request HTTP deadline.
func New(log port.Logger) *Client {
	return &Client{
		catalogBaseURL: os.Getenv("IAM_CATALOG_BASE_URL"),
		orgBaseURL:     os.Getenv("IAM_ORG_BASE_URL"),
		http: &http.Client{
			Timeout:   iamTimeout(),
			Transport: traceInjectTransport{http.DefaultTransport},
		},
		log: log,
	}
}

// NewWithConfig returns a Client with explicit base URLs.
// catalogBaseURL targets iam-catalog-admin; orgBaseURL targets iam-org-membership.
func NewWithConfig(catalogBaseURL, orgBaseURL string, log port.Logger) *Client {
	return &Client{
		catalogBaseURL: catalogBaseURL,
		orgBaseURL:     orgBaseURL,
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

// FetchPlan fetches a single plan definition from iam-catalog-admin (CAT-I2 single).
// Path: GET {IAM_CATALOG_BASE_URL}/api/v1/internal/plans/{planCode}
func (c *Client) FetchPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	url := fmt.Sprintf("%s/api/v1/internal/plans/%s", c.catalogBaseURL, planCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("iam: build request: %w", err)
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
	default:
		return nil, fmt.Errorf("iam: fetch plan %q: unexpected status %d", planCode, resp.StatusCode)
	}

	var out domain.Plan
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("iam: decode plan response: %w", err)
	}
	return &out, nil
}

// internalPlansResponse is the wire shape of CAT-I2's bulk response from iam-catalog-admin.
type internalPlansResponse struct {
	Plans []domain.Plan `json:"plans"`
}

// FetchAllPlans fetches all plan definitions in one call from iam-catalog-admin (CAT-I2 bulk).
// Path: GET {IAM_CATALOG_BASE_URL}/api/v1/internal/plans
// Used by cmd/catalog-refresh to bootstrap an empty plan_catalog_cache.
func (c *Client) FetchAllPlans(ctx context.Context) ([]*domain.Plan, error) {
	url := fmt.Sprintf("%s/api/v1/internal/plans", c.catalogBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("iam: build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("iam: fetch all plans: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("iam: fetch all plans: unexpected status %d", resp.StatusCode)
	}

	var body internalPlansResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("iam: decode all plans response: %w", err)
	}
	plans := make([]*domain.Plan, len(body.Plans))
	for i := range body.Plans {
		p := body.Plans[i]
		plans[i] = &p
	}
	return plans, nil
}

// FetchTenantSubscription fetches a tenant's current plan and state from iam-org-membership (I-17).
// Path: GET {IAM_ORG_BASE_URL}/api/v1/internal/tenants/{id}/plan-context
func (c *Client) FetchTenantSubscription(ctx context.Context, tenantID uuid.UUID) (*port.TenantSubscription, error) {
	url := fmt.Sprintf("%s/api/v1/internal/tenants/%s/plan-context", c.orgBaseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("iam: build request: %w", err)
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
	default:
		return nil, fmt.Errorf("iam: fetch subscription for %s: unexpected status %d", tenantID, resp.StatusCode)
	}

	var out port.TenantSubscription
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("iam: decode subscription response: %w", err)
	}
	return &out, nil
}
