//go:build integration

// Package postgres_test — P5-IDEMP series: IdempotencyRepository integration tests.
// These tests verify the at-most-once delivery contract (IDEMP-1..4, EVT-10)
// against a real Postgres testcontainer.
package postgres_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// idempotencyNoopPublisher discards all event enqueues (no SNS in integration tests).
type idempotencyNoopPublisher struct{}

func (idempotencyNoopPublisher) Enqueue(_ context.Context, _ *domain.DomainEvent) error {
	return nil
}

var _ port.EventPublisher = idempotencyNoopPublisher{}

// ── P5-IDEMP-001: first call processes, second call deduplicates ─────────────

// Test Case ID:      P5-IDEMP-001
// Feature:           ProcessOnce — first event processed, duplicate skipped
func TestP5Idemp001_FirstEventProcessed_SecondSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	appPool, _, _, _ := setupTestDB(t)

	repo, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "billing-entitlements-q")
	require.NoError(t, err)

	var callCount int
	fn := func(_ context.Context) error { callCount++; return nil }

	dup1, err := repo.ProcessOnce(ctx, "0d1c7d32-a256-5608-b71c-f337f9bf47fb", "TenantPlanChanged", fn)
	require.NoError(t, err)
	assert.False(t, dup1, "first call must not be a duplicate")
	assert.Equal(t, 1, callCount, "fn must be called exactly once on first delivery")

	dup2, err := repo.ProcessOnce(ctx, "0d1c7d32-a256-5608-b71c-f337f9bf47fb", "TenantPlanChanged", fn)
	require.NoError(t, err)
	assert.True(t, dup2, "second call with same event_id must be a duplicate")
	assert.Equal(t, 1, callCount, "fn must NOT be called on duplicate delivery")
}

// ── P5-IDEMP-002: fn error rolls back the dedup claim ────────────────────────

// Test Case ID:      P5-IDEMP-002
// Feature:           ProcessOnce — fn error rolls back; next attempt is NOT a duplicate
func TestP5Idemp002_FnError_ClaimRolledBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	appPool, _, _, _ := setupTestDB(t)

	repo, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "tenant-entitlements-q")
	require.NoError(t, err)

	failFn := func(_ context.Context) error { return assert.AnError }

	dup1, err := repo.ProcessOnce(ctx, "39941260-dd2b-5612-8c49-7f89db2bf6c0", "TenantSuspended", failFn)
	require.Error(t, err, "fn error must propagate")
	assert.False(t, dup1)

	var successCalled bool
	dup2, err := repo.ProcessOnce(ctx, "39941260-dd2b-5612-8c49-7f89db2bf6c0", "TenantSuspended",
		func(_ context.Context) error { successCalled = true; return nil })
	require.NoError(t, err)
	assert.False(t, dup2, "after rollback, retry must NOT be flagged as duplicate")
	assert.True(t, successCalled, "fn must be called on successful retry")
}

// ── P5-IDEMP-003: concurrent calls for the same event — exactly one wins ─────

// Test Case ID:      P5-IDEMP-003
// Feature:           ProcessOnce — concurrent duplicate race: exactly one wins
func TestP5Idemp003_ConcurrentDuplicate_ExactlyOneWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	appPool, _, _, _ := setupTestDB(t)

	repo, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "catalog-entitlements-q")
	require.NoError(t, err)

	const goroutines = 5
	var callCount int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = repo.ProcessOnce(ctx, "3e1ceb03-a876-5a82-a3da-7efc326121bf", "PlanCatalogChanged",
				func(_ context.Context) error {
					atomic.AddInt64(&callCount, 1)
					time.Sleep(5 * time.Millisecond)
					return nil
				})
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(1), callCount,
		"with concurrent duplicates fn must be called exactly once")
}

// ── P5-IDEMP-004: different consumers are independent ────────────────────────

// Test Case ID:      P5-IDEMP-004
// Feature:           ProcessOnce — same event_id on different consumers are independent
func TestP5Idemp004_DifferentConsumers_Independent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	appPool, _, _, _ := setupTestDB(t)

	pub := idempotencyNoopPublisher{}
	billing, err := pgadapter.NewIdempotencyRepository(appPool, pub, "billing-entitlements-q")
	require.NoError(t, err)
	tenant, err := pgadapter.NewIdempotencyRepository(appPool, pub, "tenant-entitlements-q")
	require.NoError(t, err)

	var billingCalls, tenantCalls int

	dup1, err := billing.ProcessOnce(ctx, "95614de4-943c-5fa1-b855-8d2c0d35e5af", "TenantPlanChanged",
		func(_ context.Context) error { billingCalls++; return nil })
	require.NoError(t, err)
	assert.False(t, dup1, "billing consumer: first call must not be duplicate")

	dup2, err := tenant.ProcessOnce(ctx, "95614de4-943c-5fa1-b855-8d2c0d35e5af", "TenantPlanChanged",
		func(_ context.Context) error { tenantCalls++; return nil })
	require.NoError(t, err)
	assert.False(t, dup2, "tenant consumer: same event_id on different consumer must not be duplicate")

	assert.Equal(t, 1, billingCalls)
	assert.Equal(t, 1, tenantCalls)
}

// ── P5-IDEMP-005: Prune removes old entries, keeps recent ones ───────────────

// Test Case ID:      P5-IDEMP-005
// Feature:           Prune — only entries older than TTL are deleted
func TestP5Idemp005_Prune_RemovesOldKeepsRecent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	appPool, seedPool, _, _ := setupTestDB(t)

	repo, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "membership-entitlements-q")
	require.NoError(t, err)

	_, err = repo.ProcessOnce(ctx, "6921fac4-6076-516b-b1a6-6727bd1fe019", "TenantMembershipsPurged",
		func(_ context.Context) error { return nil })
	require.NoError(t, err)

	_, err = repo.ProcessOnce(ctx, "a5e324eb-fcea-5c82-acf6-3ece133e26a9", "TenantMembershipsPurged",
		func(_ context.Context) error { return nil })
	require.NoError(t, err)

	// Back-date the old entry to simulate it being > 8 days old.
	err = seedPool.Exec(ctx, `
		UPDATE processed_events
		SET processed_at = now() - interval '10 days'
		WHERE event_id = '6921fac4-6076-516b-b1a6-6727bd1fe019'
		  AND consumer  = 'membership-entitlements-q'`)
	require.NoError(t, err)

	n, err := repo.Prune(ctx, 8*24*time.Hour, 100)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1), "at least the old entry must be pruned")

	// Recent entry must still deduplicate.
	dupRecent, err := repo.ProcessOnce(ctx, "a5e324eb-fcea-5c82-acf6-3ece133e26a9", "TenantMembershipsPurged",
		func(_ context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, dupRecent, "recent entry must survive prune")

	// Old entry must be reprocessable after pruning.
	var reprocessed bool
	dupOld, err := repo.ProcessOnce(ctx, "6921fac4-6076-516b-b1a6-6727bd1fe019", "TenantMembershipsPurged",
		func(_ context.Context) error { reprocessed = true; return nil })
	require.NoError(t, err)
	assert.False(t, dupOld, "pruned entry must allow reprocessing")
	assert.True(t, reprocessed)
}
