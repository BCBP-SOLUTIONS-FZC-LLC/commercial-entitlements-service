package unit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

func newCatalogService(iam port.IAMClient, cache port.Cache, temporal port.TemporalStarter) *service.CatalogService {
	return service.NewCatalogService(iam, cache, temporal).WithLogger(port.NopLogger{})
}

// ── GetPlan ────────────────────────────────────────────────────────────────

func TestGetPlan_CacheHit(t *testing.T) {
	plan := samplePlan("starter")
	cache := newFakeCache()
	_ = cache.SetPlan(context.Background(), plan)

	iam := &fakeIAMClient{
		fetchPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) {
			t.Fatal("IAM client should not be called on cache hit")
			return nil, nil
		},
	}
	svc := newCatalogService(iam, cache, nil)
	got, err := svc.GetPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
}

func TestGetPlan_CacheMiss_FetchesFromIAM(t *testing.T) {
	cache := newFakeCache() // empty
	iam := &fakeIAMClient{}
	svc := newCatalogService(iam, cache, nil)

	got, err := svc.GetPlan(context.Background(), "pro")
	require.NoError(t, err)
	assert.Equal(t, "pro", got.Code)

	// also warmed in cache
	cached, _ := cache.GetPlan(context.Background(), "pro")
	require.NotNil(t, cached)
	assert.Equal(t, "pro", cached.Code)
}

func TestGetPlan_NilCache_FetchesFromIAM(t *testing.T) {
	iam := &fakeIAMClient{}
	svc := newCatalogService(iam, nil, nil)
	got, err := svc.GetPlan(context.Background(), "enterprise")
	require.NoError(t, err)
	assert.Equal(t, "enterprise", got.Code)
}

func TestGetPlan_CacheError_FallsThroughToIAM(t *testing.T) {
	cache := newFakeCache()
	cache.getPlanFn = func(_ context.Context, _ string) (*domain.Plan, error) {
		return nil, errors.New("cache error")
	}
	iam := &fakeIAMClient{}
	svc := newCatalogService(iam, cache, nil)

	got, err := svc.GetPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
}

func TestGetPlan_IAMError(t *testing.T) {
	iam := &fakeIAMClient{
		fetchPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	svc := newCatalogService(iam, nil, nil)
	_, err := svc.GetPlan(context.Background(), "starter")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iam unavailable")
}

// ── RefreshPlan ────────────────────────────────────────────────────────────

func TestRefreshPlan_HappyPath(t *testing.T) {
	cache := newFakeCache()
	iam := &fakeIAMClient{}
	svc := newCatalogService(iam, cache, nil)

	got, err := svc.RefreshPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)

	cached, _ := cache.GetPlan(context.Background(), "starter")
	require.NotNil(t, cached)
}

func TestRefreshPlan_IAMError(t *testing.T) {
	iam := &fakeIAMClient{
		fetchPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) {
			return nil, errors.New("iam down")
		},
	}
	svc := newCatalogService(iam, nil, nil)
	_, err := svc.RefreshPlan(context.Background(), "starter")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch plan")
}

func TestRefreshPlan_CacheWriteError_NonFatal(t *testing.T) {
	cache := newFakeCache()
	cache.setPlanErr = errors.New("cache write failed")
	iam := &fakeIAMClient{}
	svc := newCatalogService(iam, cache, nil)

	got, err := svc.RefreshPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
}

func TestRefreshPlan_NilCache(t *testing.T) {
	iam := &fakeIAMClient{}
	svc := newCatalogService(iam, nil, nil)
	got, err := svc.RefreshPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
}

// ── ListPlans ──────────────────────────────────────────────────────────────

func TestListPlans_NoCatalogCache_ReturnsNil(t *testing.T) {
	svc := newCatalogService(&fakeIAMClient{}, nil, nil)
	plans, err := svc.ListPlans(context.Background())
	require.NoError(t, err)
	assert.Nil(t, plans)
}

func TestListPlans_WithCatalogCache(t *testing.T) {
	repo := newCatalogCacheRepo()
	repo.planCodes = []string{"starter", "pro", "enterprise"}
	iam := &fakeIAMClient{}
	svc := service.NewCatalogService(iam, nil, nil).WithCatalogCache(repo)

	plans, err := svc.ListPlans(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"starter", "pro", "enterprise"}, plans)
}

// ── FanoutCatalogRefresh ───────────────────────────────────────────────────

func TestFanoutCatalogRefresh_NoTemporal_NoError(t *testing.T) {
	svc := newCatalogService(&fakeIAMClient{}, nil, nil)
	err := svc.FanoutCatalogRefresh(context.Background(), "starter")
	require.NoError(t, err)
}

func TestFanoutCatalogRefresh_WithTemporal_CallsStartCatalogFanout(t *testing.T) {
	temporal := &fakeTemporalStarter{}
	svc := newCatalogService(&fakeIAMClient{}, nil, temporal)
	err := svc.FanoutCatalogRefresh(context.Background(), "pro")
	require.NoError(t, err)
	assert.Equal(t, []string{"pro"}, temporal.calls)
}

func TestFanoutCatalogRefresh_TemporalError_Propagated(t *testing.T) {
	temporal := &fakeTemporalStarter{catalogFanoutErr: errors.New("temporal down")}
	svc := newCatalogService(&fakeIAMClient{}, nil, temporal)
	err := svc.FanoutCatalogRefresh(context.Background(), "starter")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal down")
}

// ── WithCatalogCache / WithLogger ──────────────────────────────────────────

func TestCatalogService_WithLogger(t *testing.T) {
	svc := service.NewCatalogService(&fakeIAMClient{}, nil, nil).WithLogger(port.NopLogger{})
	got, err := svc.GetPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
}

// ── GetPlan coverage: cache returns plan=nil (miss) ───────────────────────

func TestGetPlan_CacheReturnsNilPlan_FetchesFromIAM(t *testing.T) {
	cache := newFakeCache()
	cache.getPlanFn = func(_ context.Context, _ string) (*domain.Plan, error) {
		return nil, nil // cache miss: nil plan, nil error
	}
	iam := &fakeIAMClient{}
	svc := newCatalogService(iam, cache, nil)

	got, err := svc.GetPlan(context.Background(), "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
}

// ── Unused planCode from IAMClient.FetchPlan ──────────────────────────────

func TestGetPlan_IAMFetchPlan_UsedOnMiss(t *testing.T) {
	var fetched string
	iam := &fakeIAMClient{
		fetchPlanFn: func(_ context.Context, code string) (*domain.Plan, error) {
			fetched = code
			return samplePlan(code), nil
		},
	}
	svc := newCatalogService(iam, nil, nil)
	_, err := svc.GetPlan(context.Background(), "enterprise")
	require.NoError(t, err)
	assert.Equal(t, "enterprise", fetched)
}

// ── CatalogService.FetchTenantSubscription (IAMClient) ────────────────────

func TestIAMClient_FetchTenantSubscription_Default(t *testing.T) {
	tenantID := uuid.New()
	iam := &fakeIAMClient{}
	sub, err := iam.FetchTenantSubscription(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, tenantID, sub.TenantID)
}

func TestIAMClient_FetchTenantSubscription_Custom(t *testing.T) {
	tenantID := uuid.New()
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(t, "enterprise", "active"), nil
		},
	}
	sub, err := iam.FetchTenantSubscription(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "enterprise", sub.PlanCode)
}
