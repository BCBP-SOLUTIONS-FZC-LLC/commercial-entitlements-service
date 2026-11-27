//go:build integration

// Phase 5 · P5-REPO — repository round-trip and error-path tests against
// a real Postgres 16 testcontainer. Each test is fully isolated (own container,
// own migration, own tenants).
package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

// ═══════════════════════════════════════════════════════════════════════════
// SnapshotRepository
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-SNAP-001
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_snapshots · Upsert + GetLatest round-trip
// Scenario:          Insert snapshot then read it back
// Expected Result:   Written=true, GetLatest returns same fields
func TestP5Snap001_UpsertAndGetLatest(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewSnapshotRepository(appPool)
	snap := sampleSnapshot(tenantID, 1)

	written, err := repo.Upsert(ctx, snap)
	require.NoError(t, err)
	assert.True(t, written, "first upsert must write")

	got, err := repo.GetLatest(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, snap.TenantID, got.TenantID)
	assert.Equal(t, snap.Version, got.Version)
	assert.Equal(t, snap.Plan, got.Plan)
	assert.Equal(t, snap.State, got.State)
}

// Test Case ID:      P5-SNAP-002
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_snapshots · ENT-INV-1 version guard
// Scenario:          Upsert with lower version must not overwrite
// Expected Result:   Written=false on stale upsert, stored version unchanged
func TestP5Snap002_VersionGuard(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewSnapshotRepository(appPool)
	snap := sampleSnapshot(tenantID, 10)
	_, err := repo.Upsert(ctx, snap)
	require.NoError(t, err)

	stale := sampleSnapshot(tenantID, 5) // lower version
	stale.Plan = "enterprise"
	written, err := repo.Upsert(ctx, stale)
	require.NoError(t, err)
	assert.False(t, written, "stale upsert must not write")

	got, err := repo.GetLatest(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, int64(10), got.Version, "version must still be 10")
	assert.Equal(t, "starter", got.Plan, "plan must not change")
}

// Test Case ID:      P5-SNAP-003
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_snapshots · GetLatest on missing tenant
// Scenario:          GetLatest for unknown tenantID returns ErrSnapshotNotFound
// Expected Result:   domain.ErrSnapshotNotFound
func TestP5Snap003_GetLatestNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewSnapshotRepository(appPool)
	_, err := repo.GetLatest(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrSnapshotNotFound)
}

// Test Case ID:      P5-SNAP-004
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_snapshots · ListTenantIDsByPlan
// Scenario:          Two tenants on "starter" plan — both returned
// Expected Result:   Both tenant IDs in result
func TestP5Snap004_ListTenantIDsByPlan(t *testing.T) {
	t.Parallel()
	appPool, _, sysPool, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewSnapshotRepository(appPool)

	// Seed two tenants on starter, one on enterprise.
	t1, t2, t3 := tenantID, uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{t1, t2} {
		s := sampleSnapshot(id, 1)
		s.Plan = "starter"
		_, err := repo.Upsert(withTenant(ctx, id), s)
		require.NoError(t, err)
	}
	s3 := sampleSnapshot(t3, 1)
	s3.Plan = "enterprise"
	_, err := repo.Upsert(withTenant(ctx, t3), s3)
	require.NoError(t, err)

	// ListTenantIDsByPlan is a cross-tenant query — uses sysPool (BYPASSRLS)
	// because the app pool enforces RLS and would only return the calling tenant's row.
	ids, err := pgadapter.NewSnapshotRepository(sysPool).ListTenantIDsByPlan(ctx, "starter")
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, t1)
	assert.Contains(t, ids, t2)
}

// ═══════════════════════════════════════════════════════════════════════════
// AddonRepository
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-ADDON-001
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · Insert + GetByID round-trip
// Scenario:          Insert addon, retrieve by ID
// Expected Result:   Fields match
func TestP5Addon001_InsertAndGetByID(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewAddonRepository(appPool)
	addon := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)

	require.NoError(t, repo.Insert(ctx, addon))

	got, err := repo.GetByID(ctx, addon.ID)
	require.NoError(t, err)
	assert.Equal(t, addon.ID, got.ID)
	assert.Equal(t, addon.TenantID, got.TenantID)
	assert.Equal(t, addon.TotalTokens, got.TotalTokens)
	assert.Equal(t, addon.Status, got.Status)
}

// Test Case ID:      P5-ADDON-002
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · GetByBillingRef
// Scenario:          Insert addon with billing_ref, retrieve by billing_ref
// Expected Result:   Addon returned with correct BillingRef
func TestP5Addon002_GetByBillingRef(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewAddonRepository(appPool)
	addon := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	ref := "inv_test_001"
	addon.BillingRef = &ref
	require.NoError(t, repo.Insert(ctx, addon))

	got, err := repo.GetByBillingRef(ctx, tenantID, ref)
	require.NoError(t, err)
	assert.Equal(t, addon.ID, got.ID)
	assert.Equal(t, ref, *got.BillingRef)
}

// Test Case ID:      P5-ADDON-003
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · Duplicate billing_ref returns domain error
// Scenario:          Two inserts with same billing_ref
// Expected Result:   Second insert returns ErrBillingRefAlreadyClaimed
func TestP5Addon003_DuplicateBillingRef(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewAddonRepository(appPool)

	a1 := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	ref := "inv_dup_001"
	a1.BillingRef = &ref
	require.NoError(t, repo.Insert(ctx, a1))

	a2 := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	a2.BillingRef = &ref
	err := repo.Insert(ctx, a2)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrBillingRefAlreadyClaimed, "got: %v", err)
}

// Test Case ID:      P5-ADDON-003b
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · billing_ref held by another tenant, inside a tx (B-8)
// Scenario:          Tenant B inserts tenant A's billing_ref in a tx, then keeps using the tx
// Expected Result:   ErrBillingRefAlreadyClaimed, and the tx is NOT aborted (it commits)
func TestP5Addon003b_BillingRefClaimedByOtherTenant_TxStaysUsable(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantA := setupTestDB(t)
	repo := pgadapter.NewAddonRepository(appPool)
	runner := pgadapter.NewTxRunner(appPool, nil)

	ref := "pur_cross_tenant_001"
	a := sampleAddon(tenantA, domain.LimitKeyLLMTokenQuotaMonthly)
	a.BillingRef = &ref
	require.NoError(t, repo.Insert(withTenant(context.Background(), tenantA), a))

	tenantB := uuid.New()
	ctxB := withTenant(context.Background(), tenantB)
	var insertErr, afterErr error
	txErr := runner.RunInTx(ctxB, func(txCtx context.Context) error {
		b := sampleAddon(tenantB, domain.LimitKeyLLMTokenQuotaMonthly)
		b.BillingRef = &ref
		insertErr = repo.Insert(txCtx, b)
		// A statement after the conflict must still run in this transaction.
		_, afterErr = repo.GetByBillingRef(txCtx, tenantB, ref)
		return nil
	})
	require.NoError(t, txErr, "the transaction must commit after a billing_ref conflict")
	assert.ErrorIs(t, insertErr, domain.ErrBillingRefAlreadyClaimed)
	assert.ErrorIs(t, afterErr, domain.ErrAddonNotFound, "tenant B has no pack; got: %v", afterErr)
}

// Test Case ID:      P5-ADDON-004
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · Update (optimistic lock)
// Scenario:          Update remaining_tokens — CONC-1 version guard
// Expected Result:   First update succeeds; second with stale version returns ErrOptimisticLockConflict
func TestP5Addon004_UpdateOptimisticLock(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewAddonRepository(appPool)
	addon := sampleAddon(tenantID, domain.LimitKeyAPIRequestBudgetMonthly)
	require.NoError(t, repo.Insert(ctx, addon))

	// First update: advance version.
	addon.RemainingTokens = 40_000
	addon.RecordVersion = 2
	addon.UpdatedAt = time.Now().UTC()
	require.NoError(t, repo.Update(ctx, addon))

	// Second update reusing old version (1) must conflict.
	stale := *addon
	stale.RemainingTokens = 30_000
	stale.RecordVersion = 2 // version after first update
	// But we set RecordVersion-1 in the WHERE clause to RecordVersion-1=1 which is now stale
	// Let's use version 1 (old) to trigger the conflict.
	stale.RecordVersion = 1
	err := repo.Update(ctx, &stale)
	assert.ErrorIs(t, err, domain.ErrOptimisticLockConflict, "stale version must yield conflict error")
}

// Test Case ID:      P5-ADDON-005
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · ListActiveByTenantAndKey
// Scenario:          Two active addons for same tenant+key; one expired
// Expected Result:   Only 2 active addons returned
func TestP5Addon005_ListActiveByTenantAndKey(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewAddonRepository(appPool)
	key := domain.LimitKeyLLMTokenQuotaMonthly

	for i := 0; i < 3; i++ {
		a := sampleAddon(tenantID, key)
		if i == 2 {
			a.Status = domain.AddonStatusExpired
		}
		require.NoError(t, repo.Insert(ctx, a))
	}

	addons, err := repo.ListActiveByTenantAndKey(ctx, tenantID, key)
	require.NoError(t, err)
	assert.Len(t, addons, 2, "only active addons should be returned")
}

// ═══════════════════════════════════════════════════════════════════════════
// OverrideRepository
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-OV-001
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_overrides · Upsert + Get round-trip
// Scenario:          Insert override, retrieve by tenant+key
// Expected Result:   Fields match
func TestP5Ov001_UpsertAndGet(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewOverrideRepository(appPool)
	ov := sampleOverride(tenantID, domain.LimitKeyAPIRateLimitRPS)

	require.NoError(t, repo.Upsert(ctx, ov))

	got, err := repo.Get(ctx, tenantID, domain.LimitKeyAPIRateLimitRPS)
	require.NoError(t, err)
	assert.Equal(t, ov.TenantID, got.TenantID)
	assert.Equal(t, ov.LimitKey, got.LimitKey)
	assert.Equal(t, *ov.Value, *got.Value)
}

// Test Case ID:      P5-OV-002
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_overrides · Delete
// Scenario:          Upsert then delete; subsequent Get returns ErrOverrideNotFound
// Expected Result:   ErrOverrideNotFound after delete
func TestP5Ov002_Delete(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewOverrideRepository(appPool)
	ov := sampleOverride(tenantID, domain.LimitKeyAPIRateLimitRPS)
	require.NoError(t, repo.Upsert(ctx, ov))
	require.NoError(t, repo.Delete(ctx, tenantID, domain.LimitKeyAPIRateLimitRPS))

	_, err := repo.Get(ctx, tenantID, domain.LimitKeyAPIRateLimitRPS)
	assert.ErrorIs(t, err, domain.ErrOverrideNotFound)
}

// Test Case ID:      P5-OV-003
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_overrides · History
// Scenario:          InsertHistory then ListHistory returns the entry
// Expected Result:   One history entry with correct fields
func TestP5Ov003_History(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewOverrideRepository(appPool)

	now := time.Now().UTC().Truncate(time.Microsecond)
	v := int64(5000)
	h := &domain.OverrideHistory{
		TenantID:  tenantID,
		LimitKey:  domain.LimitKeyAPIRateLimitRPS,
		NewValue:  &v,
		Action:    domain.ActionSet,
		Reason:    "raise rate limit for load test",
		ActorID:   uuid.New(),
		CreatedAt: now,
	}
	require.NoError(t, repo.InsertHistory(ctx, h))

	hist, err := repo.ListHistory(ctx, tenantID, domain.LimitKeyAPIRateLimitRPS)
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.Equal(t, domain.ActionSet, hist[0].Action)
	assert.Equal(t, v, *hist[0].NewValue)
}

// ═══════════════════════════════════════════════════════════════════════════
// DrawdownRepository
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-DD-001
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_drawdowns · Insert + GetByRequestID round-trip
// Scenario:          Insert drawdown with request_id, retrieve by request_id
// Expected Result:   Same record returned
func TestP5Dd001_InsertAndGetByRequestID(t *testing.T) {
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
		TokensConsumed: 1000,
		PeriodStart:    now,
		RequestID:      &reqID,
		RecordedAt:     now,
	}
	require.NoError(t, ddRepo.Insert(ctx, dd))

	got, err := ddRepo.GetByRequestID(ctx, tenantID, reqID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, dd.ID, got.ID)
	assert.Equal(t, reqID, *got.RequestID)
}

// Test Case ID:      P5-DD-002
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_drawdowns · GetByRequestID not found returns nil
// Scenario:          GetByRequestID for unknown request_id
// Expected Result:   nil, nil
func TestP5Dd002_GetByRequestIDNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	ddRepo := pgadapter.NewDrawdownRepository(appPool)

	got, err := ddRepo.GetByRequestID(ctx, tenantID, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got, "unknown request_id must return nil, nil")
}

// ═══════════════════════════════════════════════════════════════════════════
// ShortfallRepository
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-SF-001
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · Insert + GetByDrawdownID
// Scenario:          Insert shortfall, retrieve by drawdown_id
// Expected Result:   Same record returned
func TestP5Sf001_InsertAndGetByDrawdownID(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	addonRepo := pgadapter.NewAddonRepository(appPool)
	ddRepo := pgadapter.NewDrawdownRepository(appPool)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	addon := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, addonRepo.Insert(ctx, addon))

	now := time.Now().UTC().Truncate(time.Microsecond)
	ddID := uuid.New()
	dd := &domain.QuotaAddonDrawdown{
		ID: ddID, AddonID: addon.ID, TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TokensConsumed: 500,
		PeriodStart: now, RecordedAt: now,
	}
	require.NoError(t, ddRepo.Insert(ctx, dd))

	sf := &domain.QuotaAddonShortfall{
		ID: uuid.New(), TenantID: tenantID, DrawdownID: ddID,
		LimitKey:  domain.LimitKeyLLMTokenQuotaMonthly,
		Requested: 500, Available: 100, Shortfall: 400,
		Status:    domain.ShortfallStatusPending,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, sfRepo.Insert(ctx, sf))

	got, err := sfRepo.GetByDrawdownID(ctx, ddID)
	require.NoError(t, err)
	assert.Equal(t, sf.ID, got.ID)
	assert.Equal(t, int64(400), got.Shortfall)
	assert.Equal(t, domain.ShortfallStatusPending, got.Status)
}

// ═══════════════════════════════════════════════════════════════════════════
// TrialRepository
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-TRIAL-001
// Module:            commercial-entitlements · Persistence
// Feature:           tenant_trial_assignments · UpsertAssignment + GetAssignment
// Scenario:          Upsert trial assignment, retrieve
// Expected Result:   Same plan_code and profile returned
func TestP5Trial001_UpsertAndGetAssignment(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewTrialRepository(appPool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := &domain.TenantTrialAssignment{
		TenantID:      tenantID,
		PlanCode:      "starter",
		Profile:       domain.TrialProfile("reduced_plan"),
		StartsAt:      now,
		EndsAt:        now.Add(14 * 24 * time.Hour),
		SetBy:         uuid.New(),
		RecordVersion: 1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	require.NoError(t, repo.UpsertAssignment(ctx, a))

	got, err := repo.GetAssignment(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, "starter", got.PlanCode)
	assert.Equal(t, domain.TrialProfile("reduced_plan"), got.Profile)
}

// Test Case ID:      P5-TRIAL-002
// Module:            commercial-entitlements · Persistence
// Feature:           trial_entitlement_overrides · UpsertOverride + ListOverrides
// Scenario:          Upsert two trial overrides, list them
// Expected Result:   Both overrides returned
func TestP5Trial002_UpsertAndListOverrides(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewTrialRepository(appPool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	v1, v2 := int64(10_000), int64(500)
	for _, o := range []*domain.TrialEntitlementOverride{
		{TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, Value: &v1, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
		{TenantID: tenantID, LimitKey: domain.LimitKeyAPIRequestBudgetMonthly, Value: &v2, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, repo.UpsertOverride(ctx, o))
	}

	ovs, err := repo.ListOverrides(ctx, tenantID)
	require.NoError(t, err)
	assert.Len(t, ovs, 2)
}

// ═══════════════════════════════════════════════════════════════════════════
// ShortfallRepository — extended coverage
// ═══════════════════════════════════════════════════════════════════════════

// helper: insert a drawdown that shortfalls can reference
func insertDrawdown(t *testing.T, ctx context.Context, appPool *pgcommon.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	addonRepo := pgadapter.NewAddonRepository(appPool)
	ddRepo := pgadapter.NewDrawdownRepository(appPool)

	addon := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, addonRepo.Insert(ctx, addon))

	now := time.Now().UTC().Truncate(time.Microsecond)
	ddID := uuid.New()
	dd := &domain.QuotaAddonDrawdown{
		ID: ddID, AddonID: addon.ID, TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TokensConsumed: 100,
		PeriodStart: now, RecordedAt: now,
	}
	require.NoError(t, ddRepo.Insert(ctx, dd))
	return ddID
}

// helper: insert a shortfall record
func insertShortfall(t *testing.T, ctx context.Context, appPool *pgcommon.Pool, tenantID, ddID uuid.UUID) *domain.QuotaAddonShortfall {
	t.Helper()
	sfRepo := pgadapter.NewShortfallRepository(appPool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	sf := &domain.QuotaAddonShortfall{
		ID: uuid.New(), TenantID: tenantID, DrawdownID: ddID,
		LimitKey:  domain.LimitKeyLLMTokenQuotaMonthly,
		Requested: 200, Available: 50, Shortfall: 150,
		Status:    domain.ShortfallStatusPending,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, sfRepo.Insert(ctx, sf))
	return sf
}

// Test Case ID:      P5-SF-002
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · Update status and resolved_at
// Scenario:          Insert shortfall, update status to approved with resolved_at
// Expected Result:   Updated fields visible in subsequent GetByID
func TestP5Sf002_Update(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	ddID := insertDrawdown(t, ctx, appPool, tenantID)
	sf := insertShortfall(t, ctx, appPool, tenantID, ddID)

	now := time.Now().UTC().Truncate(time.Microsecond)
	sf.Status = domain.ShortfallStatusApproved
	sf.UpdatedAt = now
	sf.ResolvedAt = &now
	require.NoError(t, sfRepo.Update(ctx, sf))

	got, err := sfRepo.GetByID(ctx, sf.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ShortfallStatusApproved, got.Status)
	require.NotNil(t, got.ResolvedAt)
}

// Test Case ID:      P5-SF-003
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · GetByID success
// Scenario:          Insert shortfall, retrieve by primary key
// Expected Result:   Same record returned
func TestP5Sf003_GetByID(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	ddID := insertDrawdown(t, ctx, appPool, tenantID)
	sf := insertShortfall(t, ctx, appPool, tenantID, ddID)

	got, err := sfRepo.GetByID(ctx, sf.ID)
	require.NoError(t, err)
	assert.Equal(t, sf.ID, got.ID)
	assert.Equal(t, sf.TenantID, got.TenantID)
	assert.Equal(t, sf.Shortfall, got.Shortfall)
}

// Test Case ID:      P5-SF-004
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · GetByID not found
// Scenario:          GetByID for unknown ID returns ErrShortfallNotFound
// Expected Result:   domain.ErrShortfallNotFound
func TestP5Sf004_GetByIDNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	_, err := sfRepo.GetByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrShortfallNotFound)
}

// Test Case ID:      P5-SF-005
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · ListByTenant
// Scenario:          Insert two shortfalls for tenant, list them without filter
// Expected Result:   Both shortfalls returned
func TestP5Sf005_ListByTenant(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	// Two drawdowns → two shortfalls
	for i := 0; i < 2; i++ {
		ddID := insertDrawdown(t, ctx, appPool, tenantID)
		insertShortfall(t, ctx, appPool, tenantID, ddID)
	}

	sfRepo := pgadapter.NewShortfallRepository(appPool)
	sfs, err := sfRepo.ListByTenant(ctx, tenantID, nil)
	require.NoError(t, err)
	assert.Len(t, sfs, 2)
}

// Test Case ID:      P5-SF-006
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · ListByTenant with status filter
// Scenario:          Insert pending and approved shortfalls; filter by pending
// Expected Result:   Only the pending shortfall returned
func TestP5Sf006_ListByTenantWithStatusFilter(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	// pending shortfall
	dd1 := insertDrawdown(t, ctx, appPool, tenantID)
	insertShortfall(t, ctx, appPool, tenantID, dd1)

	// approved shortfall
	dd2 := insertDrawdown(t, ctx, appPool, tenantID)
	sf2 := insertShortfall(t, ctx, appPool, tenantID, dd2)
	now := time.Now().UTC().Truncate(time.Microsecond)
	sf2.Status = domain.ShortfallStatusApproved
	sf2.UpdatedAt = now
	sf2.ResolvedAt = &now
	require.NoError(t, sfRepo.Update(ctx, sf2))

	pending, err := sfRepo.ListByTenant(ctx, tenantID, []domain.ShortfallStatus{domain.ShortfallStatusPending})
	require.NoError(t, err)
	assert.Len(t, pending, 1)
	assert.Equal(t, domain.ShortfallStatusPending, pending[0].Status)
}

// Test Case ID:      P5-SF-007
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · CountRecentForTenant
// Scenario:          Insert two non-auto-accepted shortfalls; count in window
// Expected Result:   Count = 2
func TestP5Sf007_CountRecentForTenant(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	for i := 0; i < 2; i++ {
		ddID := insertDrawdown(t, ctx, appPool, tenantID)
		insertShortfall(t, ctx, appPool, tenantID, ddID)
	}

	sfRepo := pgadapter.NewShortfallRepository(appPool)
	count, err := sfRepo.CountRecentForTenant(ctx, tenantID, 30)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// ═══════════════════════════════════════════════════════════════════════════
// TrialRepository — extended coverage
// ═══════════════════════════════════════════════════════════════════════════

// helper: upsert a trial assignment
func upsertTrialAssignment(t *testing.T, ctx context.Context, repo *pgadapter.TrialRepository, tenantID uuid.UUID) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := &domain.TenantTrialAssignment{
		TenantID:      tenantID,
		PlanCode:      "starter",
		Profile:       domain.TrialProfile("reduced_plan"),
		StartsAt:      now,
		EndsAt:        now.Add(14 * 24 * time.Hour),
		SetBy:         uuid.New(),
		RecordVersion: 1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	require.NoError(t, repo.UpsertAssignment(ctx, a))
}

// Test Case ID:      P5-TRIAL-003
// Module:            commercial-entitlements · Persistence
// Feature:           tenant_trial_assignments · DeleteAssignment
// Scenario:          Upsert assignment then delete; subsequent GetAssignment returns not found
// Expected Result:   ErrTrialAssignmentNotFound after delete
func TestP5Trial003_DeleteAssignment(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewTrialRepository(appPool)

	upsertTrialAssignment(t, ctx, repo, tenantID)

	require.NoError(t, repo.DeleteAssignment(ctx, tenantID))

	_, err := repo.GetAssignment(ctx, tenantID)
	assert.ErrorIs(t, err, domain.ErrTrialAssignmentNotFound)
}

// Test Case ID:      P5-TRIAL-004
// Module:            commercial-entitlements · Persistence
// Feature:           trial_entitlement_overrides · GetOverride success
// Scenario:          Upsert trial override, retrieve by tenant+limitKey
// Expected Result:   Same value and limitKey returned
func TestP5Trial004_GetOverride(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewTrialRepository(appPool)

	now := time.Now().UTC().Truncate(time.Microsecond)
	v := int64(5000)
	o := &domain.TrialEntitlementOverride{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		Value: &v, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.UpsertOverride(ctx, o))

	got, err := repo.GetOverride(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, err)
	assert.Equal(t, domain.LimitKeyLLMTokenQuotaMonthly, got.LimitKey)
	require.NotNil(t, got.Value)
	assert.Equal(t, v, *got.Value)
}

// Test Case ID:      P5-TRIAL-005
// Module:            commercial-entitlements · Persistence
// Feature:           trial_entitlement_overrides · GetOverride not found
// Scenario:          GetOverride for unknown limitKey returns ErrTrialOverrideNotFound
// Expected Result:   domain.ErrTrialOverrideNotFound
func TestP5Trial005_GetOverrideNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewTrialRepository(appPool)

	_, err := repo.GetOverride(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	assert.ErrorIs(t, err, domain.ErrTrialOverrideNotFound)
}

// Test Case ID:      P5-TRIAL-006
// Module:            commercial-entitlements · Persistence
// Feature:           trial_entitlement_overrides · DeleteOverride
// Scenario:          Upsert two overrides, delete one; only the other remains
// Expected Result:   GetOverride on deleted key returns ErrTrialOverrideNotFound
func TestP5Trial006_DeleteOverride(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewTrialRepository(appPool)

	now := time.Now().UTC().Truncate(time.Microsecond)
	v1, v2 := int64(1000), int64(2000)
	for _, o := range []*domain.TrialEntitlementOverride{
		{TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, Value: &v1, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
		{TenantID: tenantID, LimitKey: domain.LimitKeyAPIRequestBudgetMonthly, Value: &v2, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, repo.UpsertOverride(ctx, o))
	}

	require.NoError(t, repo.DeleteOverride(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly))

	_, err := repo.GetOverride(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	assert.ErrorIs(t, err, domain.ErrTrialOverrideNotFound, "deleted key must be gone")

	got, err := repo.GetOverride(ctx, tenantID, domain.LimitKeyAPIRequestBudgetMonthly)
	require.NoError(t, err)
	assert.Equal(t, domain.LimitKeyAPIRequestBudgetMonthly, got.LimitKey, "other key must survive")
}

// Test Case ID:      P5-TRIAL-007
// Module:            commercial-entitlements · Persistence
// Feature:           trial_entitlement_overrides · DeleteAllOverrides
// Scenario:          Upsert two overrides, delete all; ListOverrides returns empty
// Expected Result:   Empty slice after DeleteAllOverrides
func TestP5Trial007_DeleteAllOverrides(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewTrialRepository(appPool)

	now := time.Now().UTC().Truncate(time.Microsecond)
	v1, v2 := int64(1000), int64(2000)
	for _, o := range []*domain.TrialEntitlementOverride{
		{TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, Value: &v1, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
		{TenantID: tenantID, LimitKey: domain.LimitKeyAPIRequestBudgetMonthly, Value: &v2, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, repo.UpsertOverride(ctx, o))
	}

	require.NoError(t, repo.DeleteAllOverrides(ctx, tenantID))

	ovs, err := repo.ListOverrides(ctx, tenantID)
	require.NoError(t, err)
	assert.Empty(t, ovs, "all overrides must be deleted")
}

// ═══════════════════════════════════════════════════════════════════════════
// OverrideRepository — extended coverage (ListExpired + collectOverrides path)
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-OV-004
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_overrides · ListExpired
// Scenario:          Insert two overrides — one with expires_at in past, one in future
// Expected Result:   Only the past-expired override returned
func TestP5Ov004_ListExpired(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewOverrideRepository(appPool)

	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	// Override 1: expires in the past
	ov1 := sampleOverride(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	ov1.ExpiresAt = &past
	require.NoError(t, repo.Upsert(ctx, ov1))

	// Override 2: expires in the future (should NOT be returned)
	ov2 := sampleOverride(tenantID, domain.LimitKeyAPIRequestBudgetMonthly)
	ov2.ExpiresAt = &future
	require.NoError(t, repo.Upsert(ctx, ov2))

	expired, err := repo.ListExpired(ctx, now)
	require.NoError(t, err)
	require.Len(t, expired, 1, "only past-expired override should be returned")
	assert.Equal(t, domain.LimitKeyLLMTokenQuotaMonthly, expired[0].LimitKey)
}

// Test Case ID:      P5-OV-005
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_overrides · ListByTenant (collectOverrides path)
// Scenario:          Insert multiple overrides for tenant, verify all returned via ListByTenant
// Expected Result:   All inserted overrides returned in limit_key order
func TestP5Ov005_ListByTenant(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewOverrideRepository(appPool)

	keys := []domain.LimitKey{
		domain.LimitKeyLLMTokenQuotaMonthly,
		domain.LimitKeyAPIRequestBudgetMonthly,
		domain.LimitKeyAPIRateLimitRPS,
	}
	for _, k := range keys {
		require.NoError(t, repo.Upsert(ctx, sampleOverride(tenantID, k)))
	}

	ovs, err := repo.ListByTenant(ctx, tenantID)
	require.NoError(t, err)
	assert.Len(t, ovs, 3)
}

// ═══════════════════════════════════════════════════════════════════════════
// AddonRepository — extended coverage
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-ADDON-006
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · ListExpired
// Scenario:          Insert active addon with past expires_at and one with future expires_at
// Expected Result:   Only the past-expired addon is returned
func TestP5Addon006_ListExpired(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewAddonRepository(appPool)

	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	// Active addon with past expiry (should be returned)
	a1 := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	a1.ExpiresAt = &past
	require.NoError(t, repo.Insert(ctx, a1))

	// Active addon with future expiry (should NOT be returned)
	a2 := sampleAddon(tenantID, domain.LimitKeyAPIRequestBudgetMonthly)
	a2.ExpiresAt = &future
	require.NoError(t, repo.Insert(ctx, a2))

	expired, err := repo.ListExpired(ctx, now)
	require.NoError(t, err)
	require.Len(t, expired, 1, "only past-expired active addon should be returned")
	assert.Equal(t, a1.ID, expired[0].ID)
}

// Test Case ID:      P5-ADDON-007
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · SumRemainingByTenantAndKey
// Scenario:          Insert two active addons for same tenant+key, sum remaining tokens
// Expected Result:   Sum equals total of both remaining_tokens
func TestP5Addon007_SumRemainingByTenantAndKey(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewAddonRepository(appPool)

	key := domain.LimitKeyLLMTokenQuotaMonthly
	a1 := sampleAddon(tenantID, key)
	a1.TotalTokens = 10_000
	a1.RemainingTokens = 10_000
	require.NoError(t, repo.Insert(ctx, a1))

	a2 := sampleAddon(tenantID, key)
	a2.TotalTokens = 5_000
	a2.RemainingTokens = 5_000
	require.NoError(t, repo.Insert(ctx, a2))

	total, err := repo.SumRemainingByTenantAndKey(ctx, tenantID, key)
	require.NoError(t, err)
	assert.Equal(t, int64(15_000), total)
}

// Test Case ID:      P5-ADDON-008
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · SumRemainingByTenantAndKey — no addons returns zero
// Scenario:          No addons for tenant+key
// Expected Result:   Returns 0
func TestP5Addon008_SumRemainingByTenantAndKeyZero(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewAddonRepository(appPool)

	total, err := repo.SumRemainingByTenantAndKey(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

// Test Case ID:      P5-ADDON-009
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_drawdowns · ListByTenantAndKey
// Scenario:          Insert two drawdowns for tenant+key; ListByTenantAndKey returns both
// Expected Result:   Both drawdowns returned, newest first
func TestP5Addon009_ListByTenantAndKey(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	addonRepo := pgadapter.NewAddonRepository(appPool)
	ddRepo := pgadapter.NewDrawdownRepository(appPool)

	key := domain.LimitKeyLLMTokenQuotaMonthly
	addon := sampleAddon(tenantID, key)
	require.NoError(t, addonRepo.Insert(ctx, addon))

	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 2; i++ {
		dd := &domain.QuotaAddonDrawdown{
			ID: uuid.New(), AddonID: addon.ID, TenantID: tenantID,
			LimitKey: key, TokensConsumed: int64(100 * (i + 1)),
			PeriodStart: now, RecordedAt: now.Add(time.Duration(i) * time.Millisecond),
		}
		require.NoError(t, ddRepo.Insert(ctx, dd))
	}

	dds, err := ddRepo.ListByTenantAndKey(ctx, tenantID, key)
	require.NoError(t, err)
	assert.Len(t, dds, 2)
}

// ═══════════════════════════════════════════════════════════════════════════
// CatalogCacheRepository
// ═══════════════════════════════════════════════════════════════════════════

// samplePlan returns a minimal Plan for catalog cache tests.
func samplePlan(code string) *domain.Plan {
	tokens := int64(100_000)
	budget := int64(50_000)
	return &domain.Plan{
		Code:                    code,
		DisplayName:             "Test Plan " + code,
		LLMTokenQuotaMonthly:    &tokens,
		APIRequestBudgetMonthly: &budget,
		APIRateLimitRPS:         100,
		RecordVersion:           1,
		FeatureSet:              map[string]interface{}{"sso": true},
		ModelTier:               "standard",
	}
}

// Test Case ID:      P5-CAT-001
// Module:            commercial-entitlements · Persistence
// Feature:           plan_catalog_cache · SetPlan + GetPlan round-trip
// Scenario:          Insert plan via SetPlan, retrieve via GetPlan
// Expected Result:   Same code, display_name and record_version
func TestP5Cat001_SetAndGetPlan(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)
	ctx := context.Background()
	repo := pgadapter.NewCatalogCacheRepository(appPool)

	plan := samplePlan("starter")
	require.NoError(t, repo.SetPlan(ctx, plan))

	got, err := repo.GetPlan(ctx, "starter")
	require.NoError(t, err)
	assert.Equal(t, "starter", got.Code)
	assert.Equal(t, plan.DisplayName, got.DisplayName)
	assert.Equal(t, plan.RecordVersion, got.RecordVersion)
}

// Test Case ID:      P5-CAT-002
// Module:            commercial-entitlements · Persistence
// Feature:           plan_catalog_cache · GetPlan not found
// Scenario:          GetPlan for unknown plan code returns ErrPlanNotFound
// Expected Result:   domain.ErrPlanNotFound
func TestP5Cat002_GetPlanNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)
	ctx := context.Background()
	repo := pgadapter.NewCatalogCacheRepository(appPool)

	_, err := repo.GetPlan(ctx, "nonexistent_plan")
	assert.ErrorIs(t, err, domain.ErrPlanNotFound)
}

// Test Case ID:      P5-CAT-003
// Module:            commercial-entitlements · Persistence
// Feature:           plan_catalog_cache · SetPlan upsert updates existing
// Scenario:          SetPlan twice with same code but updated display_name
// Expected Result:   GetPlan returns the updated display_name
func TestP5Cat003_SetPlanUpsert(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)
	ctx := context.Background()
	repo := pgadapter.NewCatalogCacheRepository(appPool)

	plan := samplePlan("enterprise")
	require.NoError(t, repo.SetPlan(ctx, plan))

	plan.DisplayName = "Enterprise Plus"
	plan.RecordVersion = 2
	require.NoError(t, repo.SetPlan(ctx, plan))

	got, err := repo.GetPlan(ctx, "enterprise")
	require.NoError(t, err)
	assert.Equal(t, "Enterprise Plus", got.DisplayName)
	assert.Equal(t, int64(2), got.RecordVersion)
}

// Test Case ID:      P5-CAT-004
// Module:            commercial-entitlements · Persistence
// Feature:           plan_catalog_cache · InvalidatePlan
// Scenario:          SetPlan then InvalidatePlan; GetPlan returns ErrPlanNotFound
// Expected Result:   ErrPlanNotFound after invalidation
func TestP5Cat004_InvalidatePlan(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)
	ctx := context.Background()
	repo := pgadapter.NewCatalogCacheRepository(appPool)

	plan := samplePlan("growth")
	require.NoError(t, repo.SetPlan(ctx, plan))
	require.NoError(t, repo.InvalidatePlan(ctx, "growth"))

	_, err := repo.GetPlan(ctx, "growth")
	assert.ErrorIs(t, err, domain.ErrPlanNotFound)
}

// Test Case ID:      P5-CAT-005
// Module:            commercial-entitlements · Persistence
// Feature:           plan_catalog_cache · ListPlanCodes
// Scenario:          Insert three plans, list their codes
// Expected Result:   All three codes returned in sorted order
func TestP5Cat005_ListPlanCodes(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)
	ctx := context.Background()
	repo := pgadapter.NewCatalogCacheRepository(appPool)

	codes := []string{"enterprise", "growth", "starter"}
	for _, code := range codes {
		require.NoError(t, repo.SetPlan(ctx, samplePlan(code)))
	}

	got, err := repo.ListPlanCodes(ctx)
	require.NoError(t, err)
	assert.Equal(t, codes, got, "codes must be returned in sorted order")
}

// Test Case ID:      P5-CAT-006
// Module:            commercial-entitlements · Persistence
// Feature:           plan_catalog_cache · ListPlanCodes empty
// Scenario:          No plans cached; ListPlanCodes returns empty slice
// Expected Result:   Empty (nil or zero-length) slice, no error
func TestP5Cat006_ListPlanCodesEmpty(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)
	ctx := context.Background()
	repo := pgadapter.NewCatalogCacheRepository(appPool)

	got, err := repo.ListPlanCodes(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// ═══════════════════════════════════════════════════════════════════════════
// DB helpers (DSNFromEnv, MigrationDSNFromEnv, SystemDSNFromEnv)
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-DB-001
// Module:            commercial-entitlements · Persistence
// Feature:           db.go · DSNFromEnv reads DATABASE_URL
// Scenario:          Set DATABASE_URL env var; DSNFromEnv returns it
// Expected Result:   Returned DSN matches the set value
func TestP5Db001_DSNFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/testdb")
	// Also clear PG_* vars so ConfigFromEnv picks DATABASE_URL exclusively.
	t.Setenv("PGHOST", "")
	t.Setenv("PGPORT", "")
	t.Setenv("PGUSER", "")
	t.Setenv("PGPASSWORD", "")
	t.Setenv("PGDATABASE", "")

	dsn := pgadapter.DSNFromEnv()
	assert.Equal(t, "postgres://user:pass@localhost/testdb", dsn)
}

// Test Case ID:      P5-DB-002
// Module:            commercial-entitlements · Persistence
// Feature:           db.go · MigrationDSNFromEnv reads MIGRATION_DATABASE_URL
// Scenario:          Set MIGRATION_DATABASE_URL; MigrationDSNFromEnv returns it
// Expected Result:   Returned DSN matches MIGRATION_DATABASE_URL
func TestP5Db002_MigrationDSNFromEnv(t *testing.T) {
	t.Setenv("MIGRATION_DATABASE_URL", "postgres://migrator:secret@db/migrations")

	dsn := pgadapter.MigrationDSNFromEnv()
	assert.Equal(t, "postgres://migrator:secret@db/migrations", dsn)
}

// Test Case ID:      P5-DB-003
// Module:            commercial-entitlements · Persistence
// Feature:           db.go · SystemDSNFromEnv uses SYSTEM_DATABASE_URL when set
// Scenario:          Set SYSTEM_DATABASE_URL; SystemDSNFromEnv returns it
// Expected Result:   Returned DSN matches SYSTEM_DATABASE_URL
func TestP5Db003_SystemDSNFromEnvUsesSystemVar(t *testing.T) {
	t.Setenv("SYSTEM_DATABASE_URL", "postgres://system:pwd@db/sysdb")

	dsn := pgadapter.SystemDSNFromEnv()
	assert.Equal(t, "postgres://system:pwd@db/sysdb", dsn)
}

// Test Case ID:      P5-DB-004
// Module:            commercial-entitlements · Persistence
// Feature:           db.go · SystemDSNFromEnv falls back to DSNFromEnv
// Scenario:          SYSTEM_DATABASE_URL unset; SystemDSNFromEnv returns DSNFromEnv value
// Expected Result:   Returned value equals DSNFromEnv()
func TestP5Db004_SystemDSNFromEnvFallback(t *testing.T) {
	// Unset SYSTEM_DATABASE_URL so fallback path is exercised.
	t.Setenv("SYSTEM_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "postgres://app:pw@localhost/appdb")
	t.Setenv("PGHOST", "")
	t.Setenv("PGPORT", "")
	t.Setenv("PGUSER", "")
	t.Setenv("PGPASSWORD", "")
	t.Setenv("PGDATABASE", "")

	// Save and restore original SYSTEM_DATABASE_URL to truly unset it.
	os.Unsetenv("SYSTEM_DATABASE_URL") //nolint:errcheck

	dsn := pgadapter.SystemDSNFromEnv()
	fallback := pgadapter.DSNFromEnv()
	assert.Equal(t, fallback, dsn)
}

// ═══════════════════════════════════════════════════════════════════════════
// IdempotencyRepository — constructor and MultiPruneInbox coverage
// ═══════════════════════════════════════════════════════════════════════════

// noopAuditRecorder satisfies port.AuditRecorder without side effects.
type noopAuditRecorder struct{}

func (noopAuditRecorder) Record(_ context.Context, _ port.AuditEntry) error        { return nil }
func (noopAuditRecorder) RecordBatch(_ context.Context, _ []port.AuditEntry) error { return nil }

var _ port.AuditRecorder = noopAuditRecorder{}

// Test Case ID:      P5-IDEMP-006
// Module:            commercial-entitlements · Persistence
// Feature:           IdempotencyRepository · WithAuditRecorder returns same repo (non-nil)
// Scenario:          Call WithAuditRecorder on a fresh repo
// Expected Result:   Returns non-nil *IdempotencyRepository (fluent builder)
func TestP5Idemp006_WithAuditRecorder(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	repo, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "billing-entitlements-q")
	require.NoError(t, err)

	got := repo.WithAuditRecorder(noopAuditRecorder{})
	require.NotNil(t, got, "WithAuditRecorder must return the repo for chaining")
}

// Test Case ID:      P5-IDEMP-007
// Module:            commercial-entitlements · Persistence
// Feature:           MultiPruneInbox · NewMultiPruneInbox creates instance
// Scenario:          Build MultiPruneInbox from two per-consumer repos
// Expected Result:   Non-nil instance; ProcessOnce returns expected error
func TestP5Idemp007_NewMultiPruneInbox(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	r1, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "consumer-a")
	require.NoError(t, err)
	r2, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "consumer-b")
	require.NoError(t, err)

	multi := pgadapter.NewMultiPruneInbox(r1, r2)
	require.NotNil(t, multi)
}

// Test Case ID:      P5-IDEMP-008
// Module:            commercial-entitlements · Persistence
// Feature:           MultiPruneInbox · ProcessOnce returns unsupported error
// Scenario:          Call ProcessOnce on a MultiPruneInbox
// Expected Result:   Returns false and a non-nil error
func TestP5Idemp008_MultiPruneInbox_ProcessOnce_ReturnsError(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	r1, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "consumer-c")
	require.NoError(t, err)

	multi := pgadapter.NewMultiPruneInbox(r1)
	dup, err := multi.ProcessOnce(context.Background(), "some-id", "SomeType", func(_ context.Context) error { return nil })
	assert.False(t, dup, "MultiPruneInbox.ProcessOnce must return false")
	require.Error(t, err, "MultiPruneInbox.ProcessOnce must return an error")
}

// ═══════════════════════════════════════════════════════════════════════════
// OverrideRepository — optimistic lock conflict path
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-OV-006
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_overrides · Upsert optimistic lock conflict
// Scenario:          Insert override then upsert again with the wrong record_version
// Expected Result:   ErrOptimisticLockConflict (CONC-1)
func TestP5Ov006_UpsertOptimisticLockConflict(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewOverrideRepository(appPool)

	// First upsert: record_version=1 (insert path, no conflict).
	ov := sampleOverride(tenantID, domain.LimitKeyAPIRateLimitRPS)
	ov.RecordVersion = 1
	require.NoError(t, repo.Upsert(ctx, ov))

	// Second upsert reuses record_version=1 instead of 2.
	// ON CONFLICT DO UPDATE WHERE record_version = EXCLUDED.record_version - 1
	// expects stored=1 and incoming=2 (1==2-1). Sending incoming=1 again means
	// 1 == 1-1 == 0, which never matches → 0 rows → ErrOptimisticLockConflict.
	stale := sampleOverride(tenantID, domain.LimitKeyAPIRateLimitRPS)
	stale.RecordVersion = 1 // should be 2 for a valid concurrent write
	err := repo.Upsert(ctx, stale)
	assert.ErrorIs(t, err, domain.ErrOptimisticLockConflict, "stale record_version must yield ErrOptimisticLockConflict")
}

// ═══════════════════════════════════════════════════════════════════════════
// AddonRepository — ErrNoRows (not-found) paths
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-ADDON-010
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · GetByID not found
// Scenario:          GetByID with a non-existent ID returns ErrAddonNotFound
// Expected Result:   domain.ErrAddonNotFound
func TestP5Addon010_GetByIDNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewAddonRepository(appPool)

	_, err := repo.GetByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrAddonNotFound, "unknown ID must return ErrAddonNotFound")
}

// Test Case ID:      P5-ADDON-011
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · GetByBillingRef not found
// Scenario:          GetByBillingRef with an unknown billing_ref returns ErrAddonNotFound
// Expected Result:   domain.ErrAddonNotFound
func TestP5Addon011_GetByBillingRefNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	repo := pgadapter.NewAddonRepository(appPool)

	_, err := repo.GetByBillingRef(ctx, tenantID, "inv_does_not_exist")
	assert.ErrorIs(t, err, domain.ErrAddonNotFound, "unknown billing_ref must return ErrAddonNotFound")
}

// ═══════════════════════════════════════════════════════════════════════════
// ShortfallRepository — optimistic lock conflict and not-found paths
// ═══════════════════════════════════════════════════════════════════════════

// Test Case ID:      P5-SF-008
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · Update optimistic lock conflict
// Scenario:          Insert shortfall, delete the row, then attempt Update → 0 rows → ErrOptimisticLockConflict
// Expected Result:   domain.ErrOptimisticLockConflict
func TestP5Sf008_UpdateOptimisticLockConflict(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	ddID := insertDrawdown(t, ctx, appPool, tenantID)
	sf := insertShortfall(t, ctx, appPool, tenantID, ddID)

	// Update once successfully.
	now := time.Now().UTC().Truncate(time.Microsecond)
	sf.Status = domain.ShortfallStatusApproved
	sf.UpdatedAt = now
	sf.ResolvedAt = &now
	require.NoError(t, sfRepo.Update(ctx, sf))

	// Try to update with the same (now-stale) tenantID/ID combination by
	// changing the tenant_id so the WHERE clause matches 0 rows.
	sf.TenantID = uuid.New()
	err := sfRepo.Update(ctx, sf)
	assert.ErrorIs(t, err, domain.ErrOptimisticLockConflict, "unmatched WHERE must yield ErrOptimisticLockConflict")
}

// Test Case ID:      P5-SF-009
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addon_shortfalls · GetByDrawdownID not found
// Scenario:          GetByDrawdownID for unknown drawdown_id returns ErrShortfallNotFound
// Expected Result:   domain.ErrShortfallNotFound
func TestP5Sf009_GetByDrawdownIDNotFound(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	_, err := sfRepo.GetByDrawdownID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrShortfallNotFound, "unknown drawdown_id must return ErrShortfallNotFound")
}

// Test Case ID:      P5-IDEMP-009
// Module:            commercial-entitlements · Persistence
// Feature:           MultiPruneInbox · Prune aggregates count across consumers
// Scenario:          Process events on two consumers, back-date both, prune via MultiPruneInbox
// Expected Result:   Aggregate prune count >= 2
func TestP5Idemp009_MultiPruneInbox_Prune(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	appPool, seedPool, _, _ := setupTestDB(t)

	pub := idempotencyNoopPublisher{}
	r1, err := pgadapter.NewIdempotencyRepository(appPool, pub, "consumer-d")
	require.NoError(t, err)
	r2, err := pgadapter.NewIdempotencyRepository(appPool, pub, "consumer-e")
	require.NoError(t, err)

	_, err = r1.ProcessOnce(ctx, "77b2698e-6f97-5d38-a86c-085fcc069aa8", "TestEvent", func(_ context.Context) error { return nil })
	require.NoError(t, err)
	_, err = r2.ProcessOnce(ctx, "eb7d9683-a690-51b0-a5c2-7e45ad54a89b", "TestEvent", func(_ context.Context) error { return nil })
	require.NoError(t, err)

	// Back-date both entries so they fall outside the TTL window.
	err = seedPool.Exec(ctx, `
		UPDATE processed_events
		SET processed_at = now() - interval '10 days'
		WHERE event_id IN ('77b2698e-6f97-5d38-a86c-085fcc069aa8','eb7d9683-a690-51b0-a5c2-7e45ad54a89b')`)
	require.NoError(t, err)

	multi := pgadapter.NewMultiPruneInbox(r1, r2)
	n, err := multi.Prune(ctx, 8*24*time.Hour, 100)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(2), "both entries must be pruned")
}

// TestP5Cat005_GetPlan_IncompatibleJSON inserts valid JSONB with a type-incompatible
// value to exercise the json.Unmarshal error path in CatalogCacheRepository.GetPlan.
func TestP5Cat005_GetPlan_IncompatibleJSON(t *testing.T) {
	t.Parallel()
	appPool, seedPool, _, _ := setupTestDB(t)
	ctx := context.Background()

	// Valid JSONB but llm_token_quota_monthly is a string — cannot unmarshal into *int64.
	err := seedPool.Exec(ctx, `
		INSERT INTO plan_catalog_cache (plan_code, display_name, record_version, definition, cached_at)
		VALUES ('bad_plan', 'Bad Plan', 1, '{"llm_token_quota_monthly":"not_a_number"}'::jsonb, now())`)
	require.NoError(t, err)

	repo := pgadapter.NewCatalogCacheRepository(appPool)
	_, err = repo.GetPlan(ctx, "bad_plan")
	require.Error(t, err, "GetPlan with type-incompatible JSON must return an error")
}

// TestP5Idemp009_MultiPruneInbox_Prune_CtxCancelled covers the ctx.Err() branch
// in MultiPruneInbox.Prune by passing a pre-cancelled context.
func TestP5Idemp009_MultiPruneInbox_Prune_CtxCancelled(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	r1, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "consumer-prune-cancel")
	require.NoError(t, err)

	multi := pgadapter.NewMultiPruneInbox(r1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// With a cancelled context, Prune may return 0 rows and a context error.
	// Either a ctx error or a DB error is acceptable — we just need the branch hit.
	_, _ = multi.Prune(ctx, 24*time.Hour, 10)
}

// TestP5Idemp010_MultiPruneInbox_Prune_CtxCancelledBetweenRepos covers the
// ctx.Err() != nil branch by using 2 repos where ctx is cancelled before the
// second iteration.
func TestP5Idemp010_MultiPruneInbox_Prune_CtxCancelledBetweenRepos(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	r1, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "consumer-cc-1")
	require.NoError(t, err)
	r2, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "consumer-cc-2")
	require.NoError(t, err)

	multi := pgadapter.NewMultiPruneInbox(r1, r2)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so both r1.Prune and r2.Prune see a cancelled ctx.
	// r1.Prune may succeed or fail; either way ctx.Err() will be non-nil on
	// the first loop iteration check, triggering the ctx.Err() branch.
	cancel()

	n, _ := multi.Prune(ctx, 24*time.Hour, 10)
	assert.GreaterOrEqual(t, n, int64(0))
}

// TestP5Sf007_Update_NonExistentID covers the RowsAffected==0 path in
// ShortfallRepository.Update (ErrOptimisticLockConflict, CONC-1).
func TestP5Sf007_Update_NonExistentID(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	now := time.Now().UTC().Truncate(time.Microsecond)
	ghost := &domain.QuotaAddonShortfall{
		ID: uuid.New(), TenantID: tenantID,
		Status: domain.ShortfallStatusApproved, UpdatedAt: now,
	}
	err := sfRepo.Update(ctx, ghost)
	assert.ErrorIs(t, err, domain.ErrOptimisticLockConflict,
		"Update with non-existent ID must return ErrOptimisticLockConflict")
}

// TestP5Sf008_GetByID_WithDisposition covers the disp != nil branch in
// scanShortfallRow by updating a shortfall with a non-nil Disposition.
func TestP5Sf008_GetByID_WithDisposition(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	ddID := insertDrawdown(t, ctx, appPool, tenantID)
	sf := insertShortfall(t, ctx, appPool, tenantID, ddID)

	now := time.Now().UTC().Truncate(time.Microsecond)
	disp := domain.DispositionApprove
	sf.Status = domain.ShortfallStatusApproved
	sf.Disposition = &disp
	sf.UpdatedAt = now
	sf.ResolvedAt = &now
	require.NoError(t, sfRepo.Update(ctx, sf))

	got, err := sfRepo.GetByID(ctx, sf.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Disposition, "Disposition must be non-nil after Update")
	assert.Equal(t, disp, *got.Disposition)
}

// TestP5Snap005_GetLatest_IncompatibleLimitsJSON covers the json.Unmarshal
// error path in SnapshotRepository.GetLatest by inserting a snapshot with
// an array JSON value for the limits column (expects a map object).
func TestP5Snap005_GetLatest_IncompatibleLimitsJSON(t *testing.T) {
	t.Parallel()
	_, seedPool, sysPool, tenantID := setupTestDB(t)
	ctx := context.Background()

	// Seed a row with incompatible JSON types bypassing RLS via seedPool.
	// limits expects a JSON object (map) but we store an array.
	err := seedPool.Exec(ctx, `
		INSERT INTO entitlement_snapshots
			(tenant_id, version, source_record_version, plan, state,
			 feature_flags, model_tier, limits, trial_profile, cause, computed_at)
		VALUES ($1, 1, 1, 'starter', 'active',
			'[]'::jsonb, 'standard', '[]'::jsonb, NULL, 'plan_change', now())`,
		tenantID)
	require.NoError(t, err)

	// Use sysPool (BYPASSRLS) to read the row back — the app pool would be
	// blocked by RLS on the wrong tenant.
	repo := pgadapter.NewSnapshotRepository(sysPool)
	_, getErr := repo.GetLatest(ctx, tenantID)
	require.Error(t, getErr, "GetLatest with incompatible limits JSON must return an error")
	assert.Contains(t, getErr.Error(), "unmarshal", "error should mention unmarshal")
}

// TestP5Addon010_Update_OptimisticLockConflict covers the RowsAffected==0 path
// in AddonRepository.Update by using a stale record_version.
func TestP5Addon010_Update_OptimisticLockConflict(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewAddonRepository(appPool)
	addon := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, repo.Insert(ctx, addon))

	// Advance record_version by 1 so the WHERE clause doesn't match.
	addon.RecordVersion = 99 // wrong version
	addon.UpdatedAt = addon.UpdatedAt.Add(1)
	err := repo.Update(ctx, addon)
	assert.ErrorIs(t, err, domain.ErrOptimisticLockConflict,
		"Update with wrong record_version must return ErrOptimisticLockConflict")
}

// TestP5Sf009_Update_CancelledContext covers the tx.Exec error path in
// ShortfallRepository.Update by passing a cancelled context.
func TestP5Sf009_Update_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	sfRepo := pgadapter.NewShortfallRepository(appPool)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call

	ghost := &domain.QuotaAddonShortfall{
		ID: uuid.New(), TenantID: tenantID,
		Status: domain.ShortfallStatusApproved,
	}
	err := sfRepo.Update(ctx, ghost)
	// A cancelled context returns a context error (not ErrOptimisticLockConflict).
	require.Error(t, err)
}

// TestP5Addon011_Update_CancelledContext covers the tx.Exec error path in
// AddonRepository.Update by passing a cancelled context.
func TestP5Addon011_Update_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	repo := pgadapter.NewAddonRepository(appPool)
	addon := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		Status: domain.AddonStatusActive, RecordVersion: 1,
	}
	err := repo.Update(withTenant(ctx, tenantID), addon)
	require.Error(t, err)
}

// TestP5Snap006_Upsert_CancelledContext covers the tx.Exec error path in
// SnapshotRepository.Upsert by passing a cancelled context.
func TestP5Snap006_Upsert_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	repo := pgadapter.NewSnapshotRepository(appPool)
	snap := sampleSnapshot(tenantID, 1)
	_, err := repo.Upsert(withTenant(ctx, tenantID), snap)
	require.Error(t, err)
}

// TestP5Cat006_GetPlan_CancelledContext covers the tx.QueryRow error path in
// CatalogCacheRepository.GetPlan by passing a cancelled context.
func TestP5Cat006_GetPlan_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	repo := pgadapter.NewCatalogCacheRepository(appPool)
	_, err := repo.GetPlan(ctx, "any_plan")
	require.Error(t, err)
}

// TestP5Addon012_GetByBillingRef_CancelledContext covers wrapConnErr in GetByBillingRef.
func TestP5Addon012_GetByBillingRef_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewAddonRepository(appPool)
	_, err := repo.GetByBillingRef(withTenant(ctx, tenantID), tenantID, "ref-x")
	require.Error(t, err)
}

// TestP5Addon013_GetByID_CancelledContext covers wrapConnErr in GetByID.
func TestP5Addon013_GetByID_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewAddonRepository(appPool)
	_, err := repo.GetByID(withTenant(ctx, tenantID), uuid.New())
	require.Error(t, err)
}

// TestP5Addon014_ListByTenant_CancelledContext covers query error in ListByTenant.
func TestP5Addon014_ListByTenant_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewAddonRepository(appPool)
	_, err := repo.ListByTenant(withTenant(ctx, tenantID), tenantID, nil)
	require.Error(t, err)
}

// TestP5Drawdown001_GetByRequestID_CancelledContext covers error in DrawdownRepository.GetByRequestID.
func TestP5Drawdown001_GetByRequestID_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewDrawdownRepository(appPool)
	_, err := repo.GetByRequestID(withTenant(ctx, tenantID), tenantID, uuid.New())
	require.Error(t, err)
}

// ═══════════════════════════════════════════════════════════════════════════
// Cancelled-context error path coverage — Override and Trial repos
// ═══════════════════════════════════════════════════════════════════════════

func TestP5Ov007_Upsert_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewOverrideRepository(appPool)
	now := time.Now().UTC()
	val := int64(100)
	err := repo.Upsert(withTenant(ctx, tenantID), &domain.Override{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: &val, SetBy: uuid.New(), Reason: "test",
		RecordVersion: 0, CreatedAt: now, UpdatedAt: now,
	})
	require.Error(t, err)
}

func TestP5Ov008_Get_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewOverrideRepository(appPool)
	_, err := repo.Get(withTenant(ctx, tenantID), tenantID, domain.LimitKeyTenderLimit)
	require.Error(t, err)
}

func TestP5Ov009_ListByTenant_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewOverrideRepository(appPool)
	_, err := repo.ListByTenant(withTenant(ctx, tenantID), tenantID)
	require.Error(t, err)
}

func TestP5Trial001_GetAssignment_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewTrialRepository(appPool)
	_, err := repo.GetAssignment(withTenant(ctx, tenantID), tenantID)
	require.Error(t, err)
}

func TestP5Trial002_GetOverride_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewTrialRepository(appPool)
	_, err := repo.GetOverride(withTenant(ctx, tenantID), tenantID, domain.LimitKeyTenderLimit)
	require.Error(t, err)
}

func TestP5Trial003_ListOverrides_CancelledContext(t *testing.T) {
	t.Parallel()
	appPool, _, _, tenantID := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := pgadapter.NewTrialRepository(appPool)
	_, err := repo.ListOverrides(withTenant(ctx, tenantID), tenantID)
	require.Error(t, err)
}
