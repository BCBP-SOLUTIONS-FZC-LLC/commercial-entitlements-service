// Package valkey contains unit tests for the advisory Valkey/Redis cache.
// Tests spin a real redis:7-alpine container via testcontainers so every
// method is exercised against an actual server, giving genuine hit/miss/error
// signal without mocks.
//
// Build tag: none (runs with `make test-unit` as long as Docker is available).
// Each test calls t.Parallel() following the iam-org-membership pattern.
package valkey

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── Shared container ─────────────────────────────────────────────────────────

var (
	sharedRedisAddr string
	sharedRedisOnce sync.Once
	sharedRedisCtr  testcontainers.Container
)

// redisAddr returns the host:port of the shared redis:7-alpine container,
// starting it on the first call.  The container lives for the duration of the
// test binary (cleaned up via t.Cleanup in the first caller).
func redisAddr(t *testing.T) string {
	t.Helper()
	sharedRedisOnce.Do(func() {
		ctx := context.Background()
		ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "redis:7-alpine",
				ExposedPorts: []string{"6379/tcp"},
				// The log line can precede the host port mapping being usable
				// (Docker Desktop forwards asynchronously); wait for both.
				WaitingFor: wait.ForAll(
					wait.ForLog("Ready to accept connections"),
					wait.ForListeningPort("6379/tcp"),
				),
			},
			Started: true,
		})
		if err != nil {
			// Surface the error; individual tests will call t.Fatal via require.
			sharedRedisAddr = ""
			return
		}
		sharedRedisCtr = ctr
		host, err := ctr.Host(ctx)
		if err != nil {
			sharedRedisAddr = ""
			return
		}
		port, err := ctr.MappedPort(ctx, "6379")
		if err != nil {
			sharedRedisAddr = ""
			return
		}
		sharedRedisAddr = fmt.Sprintf("%s:%s", host, port.Port())
	})

	if sharedRedisAddr == "" {
		t.Skip("redis container unavailable — skipping (Docker not running?)")
	}
	return sharedRedisAddr
}

// newTestCache builds a Cache pointed at the shared container and flushes the
// DB between tests to keep them independent.
func newTestCache(t *testing.T) *Cache {
	t.Helper()
	addr := redisAddr(t)
	c := New(addr)
	t.Cleanup(func() { _ = c.Close() })

	// Flush between tests for isolation. Use a separate client with normal
	// timeouts: the service client's 50 ms read / 100 ms dial budget is
	// deliberate (CACHE-1: the cache is advisory and must fail fast), but a
	// cold first connection under -race on Docker Desktop can exceed it,
	// which made this setup step flaky.
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second})
	defer func() { _ = admin.Close() }()
	require.NoError(t, admin.FlushDB(ctx).Err())
	// Warm the service client's pool so a test's first command doesn't pay
	// the cold-dial cost against its own tight timeouts.
	require.Eventually(t, func() bool { return c.rdb.Ping(ctx).Err() == nil }, 5*time.Second, 20*time.Millisecond)
	return c
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func sampleEffectiveSet(tenantID uuid.UUID) *domain.EffectiveSet {
	v := int64(100_000)
	return &domain.EffectiveSet{
		TenantID:     tenantID,
		Version:      1,
		PlanCode:     "starter",
		State:        domain.EntitlementStateActive,
		FeatureFlags: []string{"feature_a"},
		ModelTier:    "standard",
		Limits: map[domain.LimitKey]domain.LimitValue{
			domain.LimitKeyLLMTokenQuotaMonthly: {Value: &v},
		},
		Cause:      domain.CausePlanChange,
		ComputedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func samplePlan() *domain.Plan {
	v := int64(50_000)
	return &domain.Plan{
		Code:                 "starter",
		DisplayName:          "Starter Plan",
		LLMTokenQuotaMonthly: &v,
		APIRateLimitRPS:      10,
		ModelTier:            "standard",
		RecordVersion:        1,
	}
}

// ── GetEffectiveSet ───────────────────────────────────────────────────────────

func TestGetEffectiveSet_Hit(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()
	es := sampleEffectiveSet(tenantID)

	require.NoError(t, c.SetEffectiveSet(ctx, tenantID, es))

	got, err := c.GetEffectiveSet(ctx, tenantID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, tenantID, got.TenantID)
	assert.Equal(t, "starter", got.PlanCode)
}

func TestGetEffectiveSet_Miss(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	// Nothing stored — should return nil, nil (CACHE-1 degradation).
	got, err := c.GetEffectiveSet(ctx, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetEffectiveSet_Error_ClosedClient(t *testing.T) {
	addr := redisAddr(t)
	// Build a separate client so we can close it without affecting other tests.
	c := New(addr)
	_ = c.Close()

	ctx := context.Background()
	got, err := c.GetEffectiveSet(ctx, uuid.New())
	// CACHE-1: error degrades to nil, nil.
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetEffectiveSet_UnmarshalError(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()

	// Write invalid JSON directly so unmarshal fails.
	key := effectiveSetKey(tenantID)
	require.NoError(t, c.rdb.Set(ctx, key, []byte("not-json"), effectiveSetTTL).Err())

	got, err := c.GetEffectiveSet(ctx, tenantID)
	// CACHE-1: unmarshal failure treated as miss.
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── SetEffectiveSet ───────────────────────────────────────────────────────────

func TestSetEffectiveSet_Success(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()
	es := sampleEffectiveSet(tenantID)

	err := c.SetEffectiveSet(ctx, tenantID, es)
	require.NoError(t, err)

	// Verify the key exists and TTL is set.
	ttl, err := c.rdb.TTL(ctx, effectiveSetKey(tenantID)).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0), "expected a positive TTL")

	// Round-trip: data should be retrievable.
	got, err := c.GetEffectiveSet(ctx, tenantID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, es.PlanCode, got.PlanCode)
}

// ── InvalidateEffectiveSet ────────────────────────────────────────────────────

func TestInvalidateEffectiveSet_Success(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()

	require.NoError(t, c.SetEffectiveSet(ctx, tenantID, sampleEffectiveSet(tenantID)))

	// Invalidate then verify miss.
	require.NoError(t, c.InvalidateEffectiveSet(ctx, tenantID))

	got, err := c.GetEffectiveSet(ctx, tenantID)
	require.NoError(t, err)
	assert.Nil(t, got, "expected cache miss after invalidation")
}

// ── GetPlan ───────────────────────────────────────────────────────────────────

func TestGetPlan_Hit(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	plan := samplePlan()

	require.NoError(t, c.SetPlan(ctx, plan))

	got, err := c.GetPlan(ctx, plan.Code)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, plan.Code, got.Code)
	assert.Equal(t, plan.DisplayName, got.DisplayName)
}

func TestGetPlan_Miss(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	got, err := c.GetPlan(ctx, "nonexistent-plan")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetPlan_Error_ClosedClient(t *testing.T) {
	addr := redisAddr(t)
	c := New(addr)
	_ = c.Close()

	ctx := context.Background()
	got, err := c.GetPlan(ctx, "any-plan")
	// CACHE-1: error degrades to nil, nil.
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetPlan_UnmarshalError(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	planCode := "broken-plan"

	key := planKey(planCode)
	require.NoError(t, c.rdb.Set(ctx, key, []byte("{bad json"), planTTL).Err())

	got, err := c.GetPlan(ctx, planCode)
	// CACHE-1: unmarshal failure treated as miss.
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── SetPlan ───────────────────────────────────────────────────────────────────

func TestSetPlan_Success(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	plan := samplePlan()

	require.NoError(t, c.SetPlan(ctx, plan))

	ttl, err := c.rdb.TTL(ctx, planKey(plan.Code)).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))

	got, err := c.GetPlan(ctx, plan.Code)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, plan.ModelTier, got.ModelTier)
}

// ── InvalidatePlan ────────────────────────────────────────────────────────────

func TestInvalidatePlan_Success(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	plan := samplePlan()

	require.NoError(t, c.SetPlan(ctx, plan))
	require.NoError(t, c.InvalidatePlan(ctx, plan.Code))

	got, err := c.GetPlan(ctx, plan.Code)
	require.NoError(t, err)
	assert.Nil(t, got, "expected cache miss after plan invalidation")
}

// ── SetQuotaCeiling ───────────────────────────────────────────────────────────

func TestSetQuotaCeiling_NonNil(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()
	ceiling := int64(500_000)

	require.NoError(t, c.SetQuotaCeiling(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly, &ceiling))

	key := quotaCeilingKey(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	val, err := c.rdb.Get(ctx, key).Int64()
	require.NoError(t, err)
	assert.Equal(t, ceiling, val)
}

func TestSetQuotaCeiling_Nil_DeletesKey(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()
	ceiling := int64(999)

	// First set a value, then overwrite with nil (unlimited) — key must be removed.
	require.NoError(t, c.SetQuotaCeiling(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly, &ceiling))

	require.NoError(t, c.SetQuotaCeiling(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly, nil))

	key := quotaCeilingKey(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	err := c.rdb.Get(ctx, key).Err()
	assert.Equal(t, redis.Nil, err, "key should not exist after nil ceiling")
}

// ── ResetUsageCounter ─────────────────────────────────────────────────────────

func TestResetUsageCounter_Success(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()
	period := "2026-11"

	require.NoError(t, c.ResetUsageCounter(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly, period))

	key := usageCounterKey(tenantID, domain.LimitKeyLLMTokenQuotaMonthly, period)
	val, err := c.rdb.Get(ctx, key).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(0), val)

	ttl, err := c.rdb.TTL(ctx, key).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0), "usage counter should have a TTL")
}

// ── DeleteUsageCounters ───────────────────────────────────────────────────────

func TestDeleteUsageCounters_Success(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	period := "2026-10"
	limitKey := domain.LimitKeyAPIRequestBudgetMonthly

	// Seed 3 tenants.
	for i := 0; i < 3; i++ {
		tenantID := uuid.New()
		require.NoError(t, c.ResetUsageCounter(ctx, tenantID, limitKey, period))
	}

	deleted, err := c.DeleteUsageCounters(ctx, limitKey, period)
	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)
}

func TestDeleteUsageCounters_EmptyResult(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	deleted, err := c.DeleteUsageCounters(ctx, domain.LimitKeyLLMTokenQuotaMonthly, "2020-01")
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted, "nothing to delete should return 0")
}

func TestDeleteUsageCounters_PaginatedScan_DeletesAll(t *testing.T) {
	// Seed 150 keys to force SCAN pagination (batch size = 100).
	c := newTestCache(t)
	ctx := context.Background()
	period := "2099-12"
	limitKey := domain.LimitKeyAPIRequestBudgetMonthly

	for i := 0; i < 150; i++ {
		require.NoError(t, c.ResetUsageCounter(ctx, uuid.New(), limitKey, period))
	}

	deleted, err := c.DeleteUsageCounters(ctx, limitKey, period)
	require.NoError(t, err)
	assert.Equal(t, int64(150), deleted, "all 150 keys must be deleted across multiple SCAN pages")
}

// ── Close ─────────────────────────────────────────────────────────────────────

func TestClose_Success(t *testing.T) {
	addr := redisAddr(t)
	c := New(addr)

	// Close must not error on a healthy client.
	assert.NoError(t, c.Close())
}

// ── Health ────────────────────────────────────────────────────────────────────

func TestHealth_Success(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	assert.NoError(t, c.Health(ctx))
}

func TestHealth_Error_ClosedClient(t *testing.T) {
	addr := redisAddr(t)
	c := New(addr)
	_ = c.Close()

	ctx := context.Background()
	err := c.Health(ctx)
	assert.Error(t, err, "Health on closed client should return an error")
}

// ── Key builder correctness ───────────────────────────────────────────────────

func TestKeyBuilders(t *testing.T) {
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	assert.Equal(t, "ent:tenant:00000000-0000-0000-0000-000000000001", effectiveSetKey(tenantID))
	assert.Equal(t, "ent:plans:starter", planKey("starter"))
	assert.Equal(t, "quota:tenant:00000000-0000-0000-0000-000000000001:llm_token_quota_monthly",
		quotaCeilingKey(tenantID, domain.LimitKeyLLMTokenQuotaMonthly))
	assert.Equal(t, "quota:usage:00000000-0000-0000-0000-000000000001:llm_token_quota_monthly:2026-11",
		usageCounterKey(tenantID, domain.LimitKeyLLMTokenQuotaMonthly, "2026-11"))
}

// ── URL-form address ──────────────────────────────────────────────────────────

func TestNew_URLForm(t *testing.T) {
	addr := redisAddr(t)
	// Build a redis:// URL and confirm the client works end-to-end.
	url := fmt.Sprintf("redis://%s/0", addr)
	c := New(url)
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	// The first dial is cold and the client's budget is 100 ms (CACHE-1);
	// allow it to connect before asserting the URL form works.
	assert.Eventually(t, func() bool { return c.Health(ctx) == nil }, 5*time.Second, 20*time.Millisecond)
}

// ── WithLogger ────────────────────────────────────────────────────────────────

// nopLogger satisfies port.Logger for testing.
type nopLogger struct{}

func (nopLogger) Info(msg string, fields map[string]interface{})  {}
func (nopLogger) Warn(msg string, fields map[string]interface{})  {}
func (nopLogger) Error(msg string, fields map[string]interface{}) {}
func (nopLogger) Debug(msg string, fields map[string]interface{}) {}

func TestWithLogger_UnmarshalWarn(t *testing.T) {
	c := newTestCache(t)
	c.WithLogger(nopLogger{})
	ctx := context.Background()
	tenantID := uuid.New()

	// Seed bad JSON so the logger.Warn path is exercised inside GetEffectiveSet.
	require.NoError(t, c.rdb.Set(ctx, effectiveSetKey(tenantID), []byte("bad"), 0).Err())

	got, err := c.GetEffectiveSet(ctx, tenantID)
	require.NoError(t, err) // CACHE-1: still nil, nil
	assert.Nil(t, got)
}

// ── SetEffectiveSet round-trips EffectiveSet with AddonEligibility ────────────

func TestSetEffectiveSet_WithAddonEligibility(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	tenantID := uuid.New()
	reason := "trial"
	es := sampleEffectiveSet(tenantID)
	es.AddonEligibility = map[string]domain.AddonResourceEligibility{
		"llm_tokens": {Eligible: false, Reason: &reason},
	}

	require.NoError(t, c.SetEffectiveSet(ctx, tenantID, es))

	got, err := c.GetEffectiveSet(ctx, tenantID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Contains(t, got.AddonEligibility, "llm_tokens")
	assert.False(t, got.AddonEligibility["llm_tokens"].Eligible)
}

// ── SetPlan JSON round-trip with FeatureSet ───────────────────────────────────

func TestSetPlan_WithFeatureSet(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	plan := samplePlan()
	plan.FeatureSet = map[string]interface{}{
		"sso":            true,
		"custom_reports": false,
	}

	require.NoError(t, c.SetPlan(ctx, plan))

	got, err := c.GetPlan(ctx, plan.Code)
	require.NoError(t, err)
	require.NotNil(t, got)

	// json.Marshal/Unmarshal round-trips booleans in interface{} as bool.
	raw, _ := json.Marshal(plan.FeatureSet)
	gotRaw, _ := json.Marshal(got.FeatureSet)
	assert.JSONEq(t, string(raw), string(gotRaw))
}
