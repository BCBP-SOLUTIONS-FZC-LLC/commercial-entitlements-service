package service

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// CatalogService handles plan catalog lookups and cache warming.
// It is backed by the IAMClient for live fetches and the advisory Valkey cache.
type CatalogService struct {
	iamClient    port.IAMClient
	cache        port.Cache
	temporal     port.TemporalStarter
	catalogCache port.CatalogCacheRepository // optional; enables ListPlans
	log          port.Logger
}

// Ensure CatalogService implements port.CatalogService at compile time.
var _ port.CatalogService = (*CatalogService)(nil)

// NewCatalogService wires up the CatalogService.
func NewCatalogService(
	iamClient port.IAMClient,
	cache port.Cache,
	temporal port.TemporalStarter,
) *CatalogService {
	return &CatalogService{
		iamClient: iamClient,
		cache:     cache,
		temporal:  temporal,
	}
}

// WithLogger attaches a logger.
func (s *CatalogService) WithLogger(l port.Logger) *CatalogService {
	s.log = l
	return s
}

// WithCatalogCache attaches the Postgres-backed plan catalog cache repository,
// enabling the ListPlans method. Called in cmd/server and cmd/catalog-refresh;
// omitted in cmd/worker (no plan listing needed there).
func (s *CatalogService) WithCatalogCache(repo port.CatalogCacheRepository) *CatalogService {
	s.catalogCache = repo
	return s
}

func (s *CatalogService) logger() port.Logger {
	if s.log != nil {
		return s.log
	}
	return port.NopLogger{}
}

// GetPlan returns the cached plan, fetching from IAM on cache miss.
func (s *CatalogService) GetPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	// Advisory cache first.
	if s.cache != nil {
		if plan, err := s.cache.GetPlan(ctx, planCode); err == nil && plan != nil {
			return plan, nil
		}
	}
	return s.RefreshPlan(ctx, planCode)
}

// RefreshPlan fetches the latest plan definition from the IAM catalog service
// and updates the local Valkey cache.
func (s *CatalogService) RefreshPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	plan, err := s.iamClient.FetchPlan(ctx, planCode)
	if err != nil {
		return nil, fmt.Errorf("fetch plan %q from IAM: %w", planCode, err)
	}
	// Warm cache (best-effort).
	if s.cache != nil {
		if cacheErr := s.cache.SetPlan(ctx, plan); cacheErr != nil {
			s.logger().Warn("catalog: failed to cache plan",
				map[string]interface{}{"plan_code": planCode, "error": cacheErr.Error()})
		}
	}
	return plan, nil
}

// ListPlans returns all plan codes currently in the Postgres catalog cache.
// Returns nil when no catalog cache repository is wired (CACHE-1 — degrades gracefully).
func (s *CatalogService) ListPlans(ctx context.Context) ([]string, error) {
	if s.catalogCache == nil {
		return nil, nil
	}
	return s.catalogCache.ListPlanCodes(ctx)
}

// FanoutCatalogRefresh triggers a Temporal workflow that recomputes all tenants
// on this plan. The workflow handles pagination internally.
func (s *CatalogService) FanoutCatalogRefresh(ctx context.Context, planCode string) error {
	if s.temporal == nil {
		s.logger().Warn("catalog: no temporal starter wired, skipping fanout", map[string]interface{}{"plan_code": planCode})
		return nil
	}
	if err := s.temporal.StartCatalogFanout(ctx, planCode); err != nil {
		return fmt.Errorf("start catalog fanout workflow for plan %q: %w", planCode, err)
	}
	return nil
}
