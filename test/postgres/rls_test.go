//go:build integration

// Phase 5 · P5-RLS — Row-Level Security isolation tests.
// Verifies that one tenant cannot read another tenant's rows via the app role.
package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// Test Case ID:      P5-RLS-001
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_snapshots RLS · Tenant isolation
// Scenario:          Tenant A writes a snapshot; Tenant B's context cannot read it
// Expected Result:   GetLatest from Tenant B's context returns ErrSnapshotNotFound
func TestP5Rls001_SnapshotTenantIsolation(t *testing.T) {
	t.Parallel()
	appPool, _, tenantA := setupTestDB(t)
	tenantB := uuid.New()

	repo := pgadapter.NewSnapshotRepository(appPool)

	// Write as Tenant A.
	ctxA := withTenant(context.Background(), tenantA)
	snap := sampleSnapshot(tenantA, 1)
	_, err := repo.Upsert(ctxA, snap)
	require.NoError(t, err)

	// Read as Tenant B — RLS should block it.
	ctxB := withTenant(context.Background(), tenantB)
	_, err = repo.GetLatest(ctxB, tenantA)
	assert.ErrorIs(t, err, domain.ErrSnapshotNotFound,
		"RLS must prevent Tenant B from reading Tenant A's snapshot")
}

// Test Case ID:      P5-RLS-002
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_overrides RLS · Tenant isolation
// Scenario:          Tenant A writes an override; Tenant B's ListByTenant sees no rows
// Expected Result:   Empty slice from Tenant B context
func TestP5Rls002_OverrideTenantIsolation(t *testing.T) {
	t.Parallel()
	appPool, _, tenantA := setupTestDB(t)
	tenantB := uuid.New()

	repo := pgadapter.NewOverrideRepository(appPool)
	ctxA := withTenant(context.Background(), tenantA)
	ov := sampleOverride(tenantA, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, repo.Upsert(ctxA, ov))

	ctxB := withTenant(context.Background(), tenantB)
	ovs, err := repo.ListByTenant(ctxB, tenantA)
	require.NoError(t, err)
	assert.Empty(t, ovs, "RLS must prevent Tenant B from listing Tenant A's overrides")
}

// Test Case ID:      P5-RLS-003
// Module:            commercial-entitlements · Persistence
// Feature:           quota_addons RLS · Tenant isolation
// Scenario:          Tenant A inserts an addon; Tenant B ListByTenant returns empty
// Expected Result:   Empty slice from Tenant B context
func TestP5Rls003_AddonTenantIsolation(t *testing.T) {
	t.Parallel()
	appPool, _, tenantA := setupTestDB(t)
	tenantB := uuid.New()

	repo := pgadapter.NewAddonRepository(appPool)
	ctxA := withTenant(context.Background(), tenantA)
	addon := sampleAddon(tenantA, domain.LimitKeyAPIRequestBudgetMonthly)
	require.NoError(t, repo.Insert(ctxA, addon))

	ctxB := withTenant(context.Background(), tenantB)
	addons, err := repo.ListByTenant(ctxB, tenantA, nil)
	require.NoError(t, err)
	assert.Empty(t, addons, "RLS must prevent Tenant B from listing Tenant A's addons")
}

// Test Case ID:      P5-RLS-004
// Module:            commercial-entitlements · Persistence
// Feature:           entitlement_snapshots RLS · Tenant reads own rows
// Scenario:          Tenant A can read its own snapshot when GUC matches
// Expected Result:   GetLatest succeeds for the owning tenant
func TestP5Rls004_TenantReadsOwnSnapshot(t *testing.T) {
	t.Parallel()
	appPool, _, tenantID := setupTestDB(t)
	ctx := withTenant(context.Background(), tenantID)

	repo := pgadapter.NewSnapshotRepository(appPool)
	snap := sampleSnapshot(tenantID, 1)
	_, err := repo.Upsert(ctx, snap)
	require.NoError(t, err)

	got, err := repo.GetLatest(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, tenantID, got.TenantID, "owner must read its own snapshot")
}
