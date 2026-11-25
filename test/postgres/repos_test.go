//go:build integration

// Phase 5 · P5-REPO — repository round-trip and error-path tests against
// a real Postgres 16 testcontainer. Each test is fully isolated (own container,
// own migration, own tenants).
package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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

	ids, err := repo.ListTenantIDsByPlan(ctx, "starter")
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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

// Test Case ID:      P5-ADDON-004
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons · Update (optimistic lock)
// Scenario:          Update remaining_tokens — CONC-1 version guard
// Expected Result:   First update succeeds; second with stale version returns ErrOptimisticLockConflict
func TestP5Addon004_UpdateOptimisticLock(t *testing.T) {
	t.Parallel()
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewOverrideRepository(appPool)

	now := time.Now().UTC().Truncate(time.Microsecond)
	v := int64(5000)
	h := &domain.OverrideHistory{
		TenantID:  tenantID,
		LimitKey:  domain.LimitKeyAPIRateLimitRPS,
		NewValue:  &v,
		Action:    domain.OverrideActionSet,
		Reason:    "raise rate limit for load test",
		ActorID:   uuid.New(),
		CreatedAt: now,
	}
	require.NoError(t, repo.InsertHistory(ctx, h))

	hist, err := repo.ListHistory(ctx, tenantID, domain.LimitKeyAPIRateLimitRPS)
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.Equal(t, domain.OverrideActionSet, hist[0].Action)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
		Status:    domain.ShortfallStatusOpen,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, sfRepo.Insert(ctx, sf))

	got, err := sfRepo.GetByDrawdownID(ctx, ddID)
	require.NoError(t, err)
	assert.Equal(t, sf.ID, got.ID)
	assert.Equal(t, int64(400), got.Shortfall)
	assert.Equal(t, domain.ShortfallStatusOpen, got.Status)
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
	appPool, _, tenantID := setupTestDB(t)
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
	appPool, _, tenantID := setupTestDB(t)
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
