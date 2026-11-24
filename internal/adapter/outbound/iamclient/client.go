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

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

const defaultTimeout = 10 * time.Second

// Client is a plain net/http-based client for the IAM catalog service.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	log     port.Logger
}

// New returns a Client. baseURL and apiKey are read from env vars when empty.
func New(log port.Logger) *Client {
	baseURL := os.Getenv("IAM_CATALOG_BASE_URL")
	apiKey := os.Getenv("IAM_CATALOG_API_KEY")
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout:   defaultTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
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
			Timeout:   defaultTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		log: log,
	}
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
