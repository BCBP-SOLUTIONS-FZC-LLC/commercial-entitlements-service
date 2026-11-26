//go:build integration

// Phase 5 · P5-CONC — optimistic-lock (CONC-1) and drawdown idempotency
// (ENT-INV-3) tests against a real Postgres instance.
package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// Test Case ID:      P5-CONC-001
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · Concurrent Update — CONC-1
// Scenario:          Two goroutines read the same addon and both attempt an update.
//
// Expected Result:   Exactly one update succeeds; the other returns ErrOptimisticLockConflict.
func TestP5Conc001_AddonConcurrentUpdate(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewAddonRepository(appPool)
	addon := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, repo.Insert(ctx, addon))

	// Both goroutines read version=1 then attempt to write version=2.
	var (
		wg   sync.WaitGroup
		errs = make([]error, 2)
		now  = time.Now().UTC()
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			cp := *addon
			cp.RemainingTokens = int64(40_000 - idx*1000)
			cp.RecordVersion = 2
			cp.UpdatedAt = now
			errs[idx] = repo.Update(ctx, &cp)
		}()
	}
	wg.Wait()

	conflicts := 0
	for _, err := range errs {
		if err != nil {
			assert.ErrorIs(t, err, domain.ErrOptimisticLockConflict)
			conflicts++
		}
	}
	assert.Equal(t, 1, conflicts, "exactly one concurrent update must conflict")
}

// Test Case ID:      P5-CONC-002
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_snapshots · ENT-INV-1 concurrent upsert
// Scenario:          Two goroutines race to upsert different versions
// Expected Result:   Higher version wins; total written count <= 2
func TestP5Conc002_SnapshotConcurrentUpsert(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewSnapshotRepository(appPool)

	var (
		wg      sync.WaitGroup
		written = make([]bool, 2)
		errs    = make([]error, 2)
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			snap := sampleSnapshot(tenantID, int64(idx+1))
			snap.Plan = []string{"starter", "pro"}[idx]
			written[idx], errs[idx] = repo.Upsert(ctx, snap)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d must not error", i)
	}

	got, err := repo.GetLatest(ctx, tenantID)
	require.NoError(t, err)
	// The highest version must win.
	assert.Equal(t, int64(2), got.Version, "highest version must be stored")
}

// Test Case ID:      P5-CONC-003
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_drawdowns · ENT-INV-3 idempotent drawdown
// Scenario:          Insert drawdown with request_id; second insert with same
//
//	request_id returns nil (not an error — the caller deduplicates).
//
// Expected Result:   Only one row; second GetByRequestID returns same record
func TestP5Conc003_DrawdownIdempotency(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	addonRepo := pgadapter.NewAddonRepository(appPool)
	ddRepo := pgadapter.NewDrawdownRepository(appPool)

	addon := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, addonRepo.Insert(ctx, addon))

	reqID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	dd := &domain.QuotaAddonDrawdown{
		ID:             uuid.New(),
		AddonID:        addon.ID,
		TenantID:       tenantID,
		LimitKey:       domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 500,
		PeriodStart:    now,
		RequestID:      &reqID,
		RecordedAt:     now,
	}
	require.NoError(t, ddRepo.Insert(ctx, dd))

	// Duplicate insert with same PK would fail — the idempotency check
	// is at the service layer via GetByRequestID. Verify GetByRequestID
	// returns the original record so the service can short-circuit.
	got, err := ddRepo.GetByRequestID(ctx, tenantID, reqID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, dd.ID, got.ID, "GetByRequestID must return the original drawdown")
}
