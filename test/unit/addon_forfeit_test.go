package unit_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

// E-9: ForfeitAddonsAtPurge (CD-55 offboarding; HLD v0.69 P-5a unconverted trial).

func seedPack(t *testing.T, repo *fakeAddonRepo, tenantID uuid.UUID, status domain.QuotaAddonStatus, remaining int64) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	exp := now.Add(365 * 24 * time.Hour)
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 100_000, RemainingTokens: remaining, Status: status,
		ExpiresAt: &exp, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.Insert(context.Background(), a))
	return a.ID
}

func TestForfeitAddonsAtPurge_ForfeitsActivePacksOnly(t *testing.T) {
	rec := &capturingAuditRecorder{}
	addons := newAddonRepo()
	tx := newTxRunnerWithAuditRecorder(rec)
	effectSvc := buildEffectSvcForAddon(newSnapshotRepo(), newOverrideRepo(), addons, newTrialRepo(), tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID, other := uuid.New(), uuid.New()
	active1 := seedPack(t, addons, tenantID, domain.AddonStatusActive, 60_000)
	active2 := seedPack(t, addons, tenantID, domain.AddonStatusActive, 0)
	expired := seedPack(t, addons, tenantID, domain.AddonStatusExpired, 0)
	otherTenant := seedPack(t, addons, other, domain.AddonStatusActive, 50_000)

	n, err := svc.ForfeitAddonsAtPurge(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	for _, id := range []uuid.UUID{active1, active2} {
		a, err := addons.GetByID(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, domain.AddonStatusForfeited, a.Status)
		assert.Zero(t, a.RemainingTokens)
		assert.Equal(t, int64(100_000), a.TotalTokens, "total_tokens is immutable (ENT-D16)")
		assert.Equal(t, int64(2), a.RecordVersion)
	}
	e, _ := addons.GetByID(context.Background(), expired)
	assert.Equal(t, domain.AddonStatusExpired, e.Status)
	o, _ := addons.GetByID(context.Background(), otherTenant)
	assert.Equal(t, domain.AddonStatusActive, o.Status)
	assert.Equal(t, int64(50_000), o.RemainingTokens)

	require.Len(t, rec.entries, 2)
	forfeitedTokens := map[int64]bool{}
	for _, en := range rec.entries {
		assert.Equal(t, "billing.token_addon.revoked", en.EntryType)
		assert.Equal(t, port.AuditActor{Type: "system", ID: "commercial-entitlements"}, en.Actor)
		assert.Equal(t, "tenant_purged", en.Metadata["reason"])
		forfeitedTokens[en.Metadata["forfeited_tokens"].(int64)] = true
	}
	assert.True(t, forfeitedTokens[60_000])
	assert.True(t, forfeitedTokens[0])

	// Idempotent: a redelivered purge finds nothing active.
	n, err = svc.ForfeitAddonsAtPurge(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Len(t, rec.entries, 2)
}

func TestForfeitAddonsAtPurge_ForfeitedPackLeavesEffectiveQuota(t *testing.T) {
	addons := newAddonRepo()
	tx := newTxRunner()
	tenantID := uuid.New()
	seedPack(t, addons, tenantID, domain.AddonStatusActive, 60_000)
	effectSvc := buildEffectSvcForAddon(newSnapshotRepo(), newOverrideRepo(), addons, newTrialRepo(), tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	_, err := svc.ForfeitAddonsAtPurge(context.Background(), tenantID)
	require.NoError(t, err)
	sum, err := addons.SumRemainingByTenantAndKey(context.Background(), tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, err)
	assert.Zero(t, sum)
}
