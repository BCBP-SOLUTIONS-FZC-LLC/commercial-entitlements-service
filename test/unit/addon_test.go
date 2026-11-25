package unit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

// buildEffectSvcForAddon returns an EffectiveSetService wired with the given repos
// and an IAM client that always returns a valid subscription. Recompute will
// succeed without side-effects that interfere with addon tests.
func buildEffectSvcForAddon(snaps *fakeSnapshotRepo, overrides *fakeOverrideRepo, addons *fakeAddonRepo, trials *fakeTrialRepo, tx *fakeTxRunner) *service.EffectiveSetService {
	iam := &fakeIAMClient{}
	catalog := &fakeCatalogSvc{}
	return service.NewEffectiveSetService(snaps, overrides, addons, trials, catalog, iam, nil, tx).WithLogger(port.NopLogger{})
}

func newAddonService() (*service.AddonService, *fakeAddonRepo, *fakeDrawdownRepo, *fakeShortfallRepo, *fakeTxRunner) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := buildEffectSvcForAddon(snaps, overrides, addons, trials, tx)
	drawdowns := newDrawdownRepo()
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, drawdowns, shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})
	return svc, addons, drawdowns, shortfalls, tx
}

// ── AddAddon ───────────────────────────────────────────────────────────────

func TestAddAddon_HappyPath(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	req := port.AddAddonRequest{
		TenantID:    tenantID,
		LimitKey:    domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 100_000,
		GrantedBy:   uuid.New(),
		Reason:      "top-up",
	}
	a, err := svc.AddAddon(context.Background(), req)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, a.ID)
	assert.Equal(t, int64(100_000), a.TotalTokens)
	assert.Equal(t, int64(100_000), a.RemainingTokens)
	assert.Equal(t, domain.AddonStatusActive, a.Status)

	// persisted
	stored, err := addons.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, stored.ID)
}

func TestAddAddon_WithExpiresAt(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	req := port.AddAddonRequest{
		TenantID:    uuid.New(),
		LimitKey:    domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 50_000,
		GrantedBy:   uuid.New(),
		Reason:      "promo",
		ExpiresAt:   &future,
	}
	a, err := svc.AddAddon(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, a.ExpiresAt)
	assert.Equal(t, future.Unix(), a.ExpiresAt.Unix())
}

func TestAddAddon_TokensZero_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	req := port.AddAddonRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 0, GrantedBy: uuid.New(), Reason: "top-up",
	}
	_, err := svc.AddAddon(context.Background(), req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrTokensRequired))
}

func TestAddAddon_TokensNegative_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	req := port.AddAddonRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: -1, GrantedBy: uuid.New(), Reason: "top-up",
	}
	_, err := svc.AddAddon(context.Background(), req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrTokensRequired))
}

func TestAddAddon_ReasonEmpty_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	req := port.AddAddonRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 100, GrantedBy: uuid.New(), Reason: "",
	}
	_, err := svc.AddAddon(context.Background(), req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrReasonRequired))
}

func TestAddAddon_InvalidLimitKey_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	req := port.AddAddonRequest{
		TenantID: uuid.New(), LimitKey: "bad_key",
		TotalTokens: 100, GrantedBy: uuid.New(), Reason: "test",
	}
	_, err := svc.AddAddon(context.Background(), req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey))
}

func TestAddAddon_ExpiresAtInPast_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	past := time.Now().UTC().Add(-time.Hour)
	req := port.AddAddonRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 100, GrantedBy: uuid.New(), Reason: "test", ExpiresAt: &past,
	}
	_, err := svc.AddAddon(context.Background(), req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrExpiresAtInPast))
}

func TestAddAddon_InsertError(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	addons.insertErr = errStorage
	req := port.AddAddonRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 100, GrantedBy: uuid.New(), Reason: "test",
	}
	_, err := svc.AddAddon(context.Background(), req)
	require.Error(t, err)
}

// ── RemoveAddon ────────────────────────────────────────────────────────────

func TestRemoveAddon_HappyPath(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.RemoveAddon(context.Background(), port.RemoveAddonRequest{
		TenantID: tenantID, AddonID: a.ID, RemovedBy: uuid.New(), Reason: "cleanup",
	})
	require.NoError(t, err)

	stored, _ := addons.GetByID(context.Background(), a.ID)
	assert.Equal(t, domain.AddonStatusRevoked, stored.Status)
	assert.Equal(t, int64(0), stored.RemainingTokens)
}

func TestRemoveAddon_ReasonEmpty_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	err := svc.RemoveAddon(context.Background(), port.RemoveAddonRequest{
		TenantID: uuid.New(), AddonID: uuid.New(), RemovedBy: uuid.New(), Reason: "",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrReasonRequired))
}

func TestRemoveAddon_AddonNotFound(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	err := svc.RemoveAddon(context.Background(), port.RemoveAddonRequest{
		TenantID: uuid.New(), AddonID: uuid.New(), RemovedBy: uuid.New(), Reason: "test",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrAddonNotFound))
}

func TestRemoveAddon_WrongTenant_Error(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: uuid.New(), // different tenant
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.RemoveAddon(context.Background(), port.RemoveAddonRequest{
		TenantID: uuid.New(), AddonID: a.ID, RemovedBy: uuid.New(), Reason: "test",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrForbidden))
}

func TestRemoveAddon_PartiallyUsed_Error(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 500, // partially used
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.RemoveAddon(context.Background(), port.RemoveAddonRequest{
		TenantID: tenantID, AddonID: a.ID, RemovedBy: uuid.New(), Reason: "test",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrPackPartiallyUsed))
}

// ── ListAddons ─────────────────────────────────────────────────────────────

func TestListAddons_ReturnsAll(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	for i := range 3 {
		_ = addons.Insert(context.Background(), &domain.QuotaAddon{
			ID: uuid.New(), TenantID: tenantID,
			LimitKey:    domain.LimitKeyLLMTokenQuotaMonthly,
			TotalTokens: int64(1000 * (i + 1)), RemainingTokens: int64(1000 * (i + 1)),
			Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
		})
	}
	list, err := svc.ListAddons(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

// ── RecordDrawdown ─────────────────────────────────────────────────────────

func TestRecordDrawdown_HappyPath_FullyCovered(t *testing.T) {
	svc, addons, drawdowns, _, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 10_000, RemainingTokens: 10_000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	req := port.RecordDrawdownRequest{
		TenantID:       tenantID,
		LimitKey:       domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 1_000,
		PeriodStart:    time.Now().UTC().Truncate(24 * time.Hour),
	}
	d, err := svc.RecordDrawdown(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000), d.TokensConsumed)

	// addon remaining decremented
	stored, _ := addons.GetByID(context.Background(), a.ID)
	assert.Equal(t, int64(9_000), stored.RemainingTokens)

	// drawdown recorded
	list, _ := drawdowns.ListByTenantAndKey(context.Background(), tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	assert.Len(t, list, 1)
}

func TestRecordDrawdown_Idempotent_DuplicateRequestID(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 10_000, RemainingTokens: 10_000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	reqID := uuid.New()
	req := port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 500, PeriodStart: now, RequestID: &reqID,
	}
	d1, err := svc.RecordDrawdown(context.Background(), req)
	require.NoError(t, err)

	// Second call with same requestID returns existing
	d2, err := svc.RecordDrawdown(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, d1.ID, d2.ID)

	// Addon should only be decremented once
	stored, _ := addons.GetByID(context.Background(), a.ID)
	assert.Equal(t, int64(9_500), stored.RemainingTokens)
}

func TestRecordDrawdown_PeriodStartZero_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	_, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TokensConsumed: 100,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidPeriodStart))
}

func TestRecordDrawdown_TokensZero_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	_, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 0, PeriodStart: time.Now(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrTokensRequired))
}

func TestRecordDrawdown_InvalidLimitKey_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	_, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: uuid.New(), LimitKey: "bad_key",
		TokensConsumed: 100, PeriodStart: time.Now(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey))
}

func TestRecordDrawdown_Shortfall_AutoAccepted(t *testing.T) {
	svc, addons, _, shortfalls, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	// Only 950 tokens available → 50 token shortfall (5% of 1000, within threshold)
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 950, RemainingTokens: 950,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	req := port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 1000, PeriodStart: now,
	}
	d, err := svc.RecordDrawdown(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int64(950), d.TokensConsumed)

	sfList, _ := shortfalls.ListByTenant(context.Background(), tenantID, nil)
	require.Len(t, sfList, 1)
	assert.Equal(t, domain.ShortfallStatusAutoAccepted, sfList[0].Status)
}

func TestRecordDrawdown_Shortfall_Escalated(t *testing.T) {
	svc, addons, _, shortfalls, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	// Only 500 tokens available → 500 shortfall (50% of 1000, exceeds threshold)
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 500,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	req := port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 1000, PeriodStart: now,
	}
	d, err := svc.RecordDrawdown(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int64(500), d.TokensConsumed)

	sfList, _ := shortfalls.ListByTenant(context.Background(), tenantID, nil)
	require.Len(t, sfList, 1)
	assert.Equal(t, domain.ShortfallStatusPendingApproval, sfList[0].Status)
}

func TestRecordDrawdown_Shortfall_EscalatedDueToRecentCount(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := buildEffectSvcForAddon(snaps, overrides, addons, trials, tx)

	shortfalls := newShortfallRepo()
	shortfalls.countFn = func(_ uuid.UUID, _ int) (int, error) {
		return 5, nil // 5 recent shortfalls → escalate
	}
	drawdowns := newDrawdownRepo()
	svc := service.NewAddonService(addons, drawdowns, shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	// 50 token shortfall within pct/abs threshold but recent count > 0 → escalate
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 950, RemainingTokens: 950,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	d, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 1000, PeriodStart: now,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(950), d.TokensConsumed)

	sfList, _ := shortfalls.ListByTenant(context.Background(), tenantID, nil)
	require.Len(t, sfList, 1)
	assert.Equal(t, domain.ShortfallStatusPendingApproval, sfList[0].Status)
}

func TestRecordDrawdown_NoActiveAddons_FullShortfall(t *testing.T) {
	svc, _, _, shortfalls, _ := newAddonService()
	tenantID := uuid.New()

	d, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 100, PeriodStart: time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), d.TokensConsumed)

	sfList, _ := shortfalls.ListByTenant(context.Background(), tenantID, nil)
	assert.Len(t, sfList, 1)
}

func TestRecordDrawdown_AddonConsumedToZero_MarkedConsumed(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 500,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	_, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 500, PeriodStart: now,
	})
	require.NoError(t, err)

	stored, _ := addons.GetByID(context.Background(), a.ID)
	assert.Equal(t, domain.AddonStatusConsumed, stored.Status)
}

func TestRecordDrawdown_ObservabilityHooks_Called(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := buildEffectSvcForAddon(snaps, overrides, addons, trials, tx)
	drawdowns := newDrawdownRepo()
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, drawdowns, shortfalls, effectSvc, tx)

	var drawdownCalled, shortfallCalled bool
	svc.WithAddonMetricsHooks(
		func(_ domain.LimitKey, hadShortfall bool) { drawdownCalled = true },
		func(_ domain.LimitKey, _ domain.ShortfallStatus) { shortfallCalled = true },
	)

	tenantID := uuid.New()
	now := time.Now().UTC()
	// No addons → full shortfall, both hooks fire
	_, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 100, PeriodStart: now,
	})
	require.NoError(t, err)
	assert.True(t, drawdownCalled)
	assert.True(t, shortfallCalled)
}

// ── ListDrawdowns ──────────────────────────────────────────────────────────

func TestListDrawdowns_Delegates(t *testing.T) {
	svc, addons, drawdowns, _, _ := newAddonService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyAPIRequestBudgetMonthly, TotalTokens: 5000, RemainingTokens: 5000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	_, _ = svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyAPIRequestBudgetMonthly,
		TokensConsumed: 100, PeriodStart: now,
	})

	list, err := svc.ListDrawdowns(context.Background(), tenantID, domain.LimitKeyAPIRequestBudgetMonthly)
	require.NoError(t, err)
	_ = drawdowns
	assert.NotEmpty(t, list)
}

// ── ResolveShortfall ───────────────────────────────────────────────────────

func TestResolveShortfall_Approve(t *testing.T) {
	svc, _, _, shortfalls, _ := newAddonService()
	tenantID := uuid.New()
	sfID := uuid.New()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: tenantID, Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}

	ref := "PO-12345"
	sf, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
		ShortfallID:    sfID,
		Disposition:    domain.DispositionApprove,
		DispositionBy:  uuid.New(),
		DispositionRef: &ref,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.ShortfallStatusApproved, sf.Status)
	assert.NotNil(t, sf.ResolvedAt)
}

func TestResolveShortfall_Reject(t *testing.T) {
	svc, _, _, shortfalls, _ := newAddonService()
	sfID := uuid.New()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: uuid.New(), Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}

	sf, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
		ShortfallID:   sfID,
		Disposition:   domain.DispositionReject,
		DispositionBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.Equal(t, domain.ShortfallStatusRejected, sf.Status)
}

func TestResolveShortfall_NotFound(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	_, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
		ShortfallID: uuid.New(), Disposition: domain.DispositionReject, DispositionBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrShortfallNotFound))
}

func TestResolveShortfall_AlreadyResolved_Error(t *testing.T) {
	for _, status := range []domain.ShortfallStatus{
		domain.ShortfallStatusApproved,
		domain.ShortfallStatusRejected,
		domain.ShortfallStatusAutoAccepted,
	} {
		svc, _, _, shortfalls, _ := newAddonService()
		sfID := uuid.New()
		shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
			ID: sfID, TenantID: uuid.New(), Status: status,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		_, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
			ShortfallID: sfID, Disposition: domain.DispositionReject, DispositionBy: uuid.New(),
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, domain.ErrShortfallAlreadyResolved))
	}
}

func TestResolveShortfall_ApproveWithoutRef_Error(t *testing.T) {
	svc, _, _, shortfalls, _ := newAddonService()
	sfID := uuid.New()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: uuid.New(), Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	_, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
		ShortfallID: sfID, Disposition: domain.DispositionApprove, DispositionBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrDispositionRefRequired))
}

func TestResolveShortfall_InvalidDisposition_Error(t *testing.T) {
	svc, _, _, shortfalls, _ := newAddonService()
	sfID := uuid.New()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: uuid.New(), Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	_, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
		ShortfallID: sfID, Disposition: "invalid", DispositionBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidAction))
}

// ── ListShortfalls / GetShortfall ──────────────────────────────────────────

func TestListShortfalls_Delegates(t *testing.T) {
	svc, _, _, shortfalls, _ := newAddonService()
	tenantID := uuid.New()
	sfID := uuid.New()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: tenantID, Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	list, err := svc.ListShortfalls(context.Background(), tenantID, []domain.ShortfallStatus{domain.ShortfallStatusPendingApproval})
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestGetShortfall_Found(t *testing.T) {
	svc, _, _, shortfalls, _ := newAddonService()
	sfID := uuid.New()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: uuid.New(), Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	sf, err := svc.GetShortfall(context.Background(), sfID)
	require.NoError(t, err)
	assert.Equal(t, sfID, sf.ID)
}

func TestGetShortfall_NotFound(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	_, err := svc.GetShortfall(context.Background(), uuid.New())
	require.Error(t, err)
}

// ── ExpireAddons ───────────────────────────────────────────────────────────

func TestExpireAddons_NoExpired(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	count, err := svc.ExpireAddons(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestExpireAddons_ExpiresOne(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := buildEffectSvcForAddon(snaps, overrides, addons, trials, tx)
	drawdowns := newDrawdownRepo()
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, drawdowns, shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	expired := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 300,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	addons.expiredList = []domain.QuotaAddon{*expired}
	_ = addons.Insert(context.Background(), expired)

	count, err := svc.ExpireAddons(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	stored, _ := addons.GetByID(context.Background(), expired.ID)
	assert.Equal(t, domain.AddonStatusExpired, stored.Status)
	assert.Equal(t, int64(0), stored.RemainingTokens)
}

// ── ApplyBillingPurchase ───────────────────────────────────────────────────

func TestApplyBillingPurchase_HappyPath(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	req := port.ApplyBillingPurchaseRequest{
		TenantID:    tenantID,
		Resource:    "llm_tokens",
		Delta:       100_000,
		BillingRef:  "inv_001",
		PurchasedAt: time.Now().UTC(),
	}
	err := svc.ApplyBillingPurchase(context.Background(), req)
	require.NoError(t, err)

	list, _ := addons.ListByTenant(context.Background(), tenantID, nil)
	require.Len(t, list, 1)
	assert.Equal(t, "inv_001", *list[0].BillingRef)
}

func TestApplyBillingPurchase_Idempotent(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	req := port.ApplyBillingPurchaseRequest{
		TenantID: tenantID, Resource: "llm_tokens", Delta: 100_000,
		BillingRef: "inv_002", PurchasedAt: time.Now().UTC(),
	}
	require.NoError(t, svc.ApplyBillingPurchase(context.Background(), req))
	require.NoError(t, svc.ApplyBillingPurchase(context.Background(), req))

	list, _ := addons.ListByTenant(context.Background(), tenantID, nil)
	assert.Len(t, list, 1) // only one created
}

func TestApplyBillingPurchase_DeltaNotPositive_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	err := svc.ApplyBillingPurchase(context.Background(), port.ApplyBillingPurchaseRequest{
		TenantID: uuid.New(), Resource: "llm_tokens", Delta: 0,
		BillingRef: "inv", PurchasedAt: time.Now(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidValue))
}

func TestApplyBillingPurchase_NoBillingRef_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	err := svc.ApplyBillingPurchase(context.Background(), port.ApplyBillingPurchaseRequest{
		TenantID: uuid.New(), Resource: "llm_tokens", Delta: 100,
		BillingRef: "", PurchasedAt: time.Now(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidValue))
}

func TestApplyBillingPurchase_UnknownResource_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	err := svc.ApplyBillingPurchase(context.Background(), port.ApplyBillingPurchaseRequest{
		TenantID: uuid.New(), Resource: "storage_gb", Delta: 100,
		BillingRef: "inv", PurchasedAt: time.Now(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey))
}

// ── ApplyBillingRefund ─────────────────────────────────────────────────────

func TestApplyBillingRefund_HappyPath(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	purchaseRef := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 10_000, RemainingTokens: 10_000,
		BillingRef: strptr(purchaseRef.String()),
		Status:     domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.ApplyBillingRefund(context.Background(), port.ApplyBillingRefundRequest{
		TenantID:    tenantID,
		PurchaseRef: purchaseRef,
		Delta:       -3_000,
		BillingRef:  "ref_xyz",
	})
	require.NoError(t, err)

	stored, _ := addons.GetByID(context.Background(), a.ID)
	assert.Equal(t, int64(7_000), stored.RemainingTokens)
	assert.Equal(t, int64(7_000), stored.TotalTokens)
}

func TestApplyBillingRefund_DeltaNotNegative_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	err := svc.ApplyBillingRefund(context.Background(), port.ApplyBillingRefundRequest{
		TenantID: uuid.New(), PurchaseRef: uuid.New(), Delta: 100, BillingRef: "ref",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidValue))
}

func TestApplyBillingRefund_AddonNotFound_Error(t *testing.T) {
	svc, _, _, _, _ := newAddonService()
	err := svc.ApplyBillingRefund(context.Background(), port.ApplyBillingRefundRequest{
		TenantID: uuid.New(), PurchaseRef: uuid.New(), Delta: -100, BillingRef: "ref",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrAddonNotFound))
}

func TestApplyBillingRefund_RefundExceedsRemaining_CappedAtRemaining(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	purchaseRef := uuid.New()
	now := time.Now().UTC()
	// Only 300 remaining tokens, refund is 1000
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 300,
		BillingRef: strptr(purchaseRef.String()),
		Status:     domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.ApplyBillingRefund(context.Background(), port.ApplyBillingRefundRequest{
		TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -1000, BillingRef: "ref",
	})
	require.NoError(t, err)

	stored, _ := addons.GetByID(context.Background(), a.ID)
	assert.Equal(t, int64(0), stored.RemainingTokens)
}

func TestApplyBillingRefund_ZeroRemaining_IsNoop(t *testing.T) {
	svc, addons, _, _, _ := newAddonService()
	tenantID := uuid.New()
	purchaseRef := uuid.New()
	now := time.Now().UTC()
	// All tokens consumed → no unused tokens to remove
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 0,
		BillingRef: strptr(purchaseRef.String()),
		Status:     domain.AddonStatusConsumed, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.ApplyBillingRefund(context.Background(), port.ApplyBillingRefundRequest{
		TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -500, BillingRef: "ref",
	})
	require.NoError(t, err)
	// Record stays at 0
	stored, _ := addons.GetByID(context.Background(), a.ID)
	assert.Equal(t, int64(0), stored.RemainingTokens)
}
