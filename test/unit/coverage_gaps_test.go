// coverage_gaps_test.go fills specific coverage gaps identified by
// go tool cover -func across the internal/core/... packages.
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

// ── logger() nil branch — EffectiveSetService ─────────────────────────────
// When a service is created without .WithLogger(), s.log==nil;
// the third statement `return port.NopLogger{}` is missed unless we
// exercise a code-path that calls logger().

func TestEffectiveSetService_Logger_NilBranch(t *testing.T) {
	tenantID := uuid.New()
	// Service without WithLogger → s.log == nil
	svc := service.NewEffectiveSetService(
		newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(),
		&fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner(),
	)
	// First recompute writes snapshot
	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	// Second identical recompute → ENT-INV-6 noop → logger().Debug called with nil log
	_, err = svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
}

// ── logger() nil branch — CatalogService ─────────────────────────────────

func TestCatalogService_Logger_NilBranch(t *testing.T) {
	cache := newFakeCache()
	cache.setPlanErr = errors.New("cache write fails")
	// Service without WithLogger → warn goes through nil logger
	svc := service.NewCatalogService(&fakeIAMClient{}, cache, nil)
	_, err := svc.RefreshPlan(context.Background(), "starter")
	require.NoError(t, err) // plan fetched ok, cache write warn is non-fatal
}

// ── logger() nil branch — OverrideService ────────────────────────────────

func TestOverrideService_Logger_NilBranch(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	// IAM always fails so recompute always fails → logger().Warn triggered
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	// Override service without WithLogger
	ovSvc := service.NewOverrideService(overrides, effectSvc, tx)

	tenantID := uuid.New()
	// SetOverride succeeds; recompute fails → logger().Warn exercised with nil log
	ov, err := ovSvc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(10), Reason: "gap test", SetBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.NotNil(t, ov)
}

// ── logger() nil branch — TrialService ───────────────────────────────────

func TestTrialService_Logger_NilBranch(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	// Trial service without WithLogger → s.log == nil
	trialSvc := service.NewTrialService(trials, effectSvc, tx)

	tenantID := uuid.New()
	now := time.Now().UTC()
	// AssignTrialProfile succeeds; recompute fails → logger().Warn with nil log
	a, err := trialSvc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		TenantID: tenantID, PlanCode: "starter", Profile: domain.TrialProfileFullPlan,
		StartsAt: now, EndsAt: now.Add(24 * time.Hour), SetBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.NotNil(t, a)
}

// ── logger() nil branch — AddonService ───────────────────────────────────

func TestAddonService_Logger_NilBranch(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	// Addon service without WithLogger
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx)

	tenantID := uuid.New()
	// AddAddon succeeds; recompute fails → logger().Warn with nil log
	a, err := svc.AddAddon(context.Background(), port.AddAddonRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 1000, GrantedBy: uuid.New(), Reason: "gap test",
	})
	require.NoError(t, err)
	assert.NotNil(t, a)
}

// ── sortAddonsByExpiry: all comparison branches ───────────────────────────
// Exercised indirectly through RecordDrawdown with 2+ addons.

func TestSortAddonsByExpiry_EarlierExpirysFirst(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	soon := now.Add(24 * time.Hour)  // earlier expiry
	later := now.Add(48 * time.Hour) // later expiry

	// Addon A: smaller expiry (should be drained first)
	addonA := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 500,
		Status: domain.AddonStatusActive, ExpiresAt: &soon,
		RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	// Addon B: larger expiry (should be drained second)
	addonB := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, ExpiresAt: &later,
		RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), addonA)
	_ = addons.Insert(context.Background(), addonB)

	// Request 600 tokens — should drain A(500) first, then 100 from B
	d, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 600, PeriodStart: now,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(600), d.TokensConsumed)

	// A should be fully consumed
	storedA, _ := addons.GetByID(context.Background(), addonA.ID)
	assert.Equal(t, int64(0), storedA.RemainingTokens)
	assert.Equal(t, domain.AddonStatusConsumed, storedA.Status)

	// B should have 900 remaining
	storedB, _ := addons.GetByID(context.Background(), addonB.ID)
	assert.Equal(t, int64(900), storedB.RemainingTokens)
}

func TestSortAddonsByExpiry_NilExpiryLastAfterNonNil(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	soon := now.Add(24 * time.Hour)

	// Addon A: nil expiry (never expires, sort last)
	addonA := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, ExpiresAt: nil,
		RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	// Addon B: non-nil expiry (sort first)
	addonB := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 500,
		Status: domain.AddonStatusActive, ExpiresAt: &soon,
		RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), addonA)
	_ = addons.Insert(context.Background(), addonB)

	// Request 600: should drain B(500) first (non-nil expiry), then 100 from A
	d, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 600, PeriodStart: now,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(600), d.TokensConsumed)

	storedB, _ := addons.GetByID(context.Background(), addonB.ID)
	assert.Equal(t, int64(0), storedB.RemainingTokens)

	storedA, _ := addons.GetByID(context.Background(), addonA.ID)
	assert.Equal(t, int64(900), storedA.RemainingTokens)
}

func TestSortAddonsByExpiry_BothNilExpiry(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()

	// Both addons have nil expiry
	for i := range 2 {
		_ = addons.Insert(context.Background(), &domain.QuotaAddon{
			ID: uuid.New(), TenantID: tenantID,
			LimitKey:    domain.LimitKeyLLMTokenQuotaMonthly,
			TotalTokens: int64(500 * (i + 1)), RemainingTokens: int64(500 * (i + 1)),
			Status: domain.AddonStatusActive, ExpiresAt: nil,
			RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
		})
	}

	// Just exercise the sort (both nil → ai==nil && aj==nil branch)
	d, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 300, PeriodStart: now,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(300), d.TokensConsumed)
}

// ── PurgeTrialForTenant: tx error paths ───────────────────────────────────

func TestPurgeTrialForTenant_DeleteAllOverrides_Error(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})

	trials.deleteAllOvErr = errStorage
	err := svc.PurgeTrialForTenant(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete trial overrides")
}

func TestPurgeTrialForTenant_DeleteAssignment_Error(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})

	trials.deleteAssignErr = errStorage
	err := svc.PurgeTrialForTenant(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete trial assignment")
}

func TestPurgeTrialForTenant_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})

	// PurgeTrialForTenant succeeds even when recompute fails
	err := svc.PurgeTrialForTenant(context.Background(), uuid.New())
	require.NoError(t, err)
}

// ── RemoveTrialOverride: tx error ─────────────────────────────────────────

func TestRemoveTrialOverride_TxError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := &fakeTxRunner{pub: &fakePublisher{}, err: errStorage}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})

	err := svc.RemoveTrialOverride(context.Background(), port.RemoveTrialOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyAPIRateLimitRPS,
	})
	require.Error(t, err)
}

// ── RemoveTrialOverride: recompute fails non-fatal ─────────────────────────

func TestRemoveTrialOverride_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	// Set an override first so delete works
	_ = trials.UpsertOverride(context.Background(), &domain.TrialEntitlementOverride{
		TenantID: tenantID, LimitKey: domain.LimitKeyAPIRateLimitRPS, Value: i64ptr(50), SetBy: uuid.New(),
	})

	err := svc.RemoveTrialOverride(context.Background(), port.RemoveTrialOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyAPIRateLimitRPS,
	})
	require.NoError(t, err)
}

// ── SetTrialOverride: recompute fails non-fatal ───────────────────────────

func TestSetTrialOverride_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})

	ov, err := svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, Value: i64ptr(5), SetBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.NotNil(t, ov)
}

// ── ExpireAddons: update error (addon update fails but continues) ─────────

func TestExpireAddons_UpdateError_ContinuesOtherEntries(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	now := time.Now().UTC()
	expired := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: uuid.New(),
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 300,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	addons.expiredList = []domain.QuotaAddon{*expired}
	_ = addons.Insert(context.Background(), expired)
	addons.updateErr = errStorage // update fails → addon skipped

	count, err := svc.ExpireAddons(context.Background())
	require.NoError(t, err) // outer method returns nil even if individual addon fails
	assert.Equal(t, 0, count)
}

func TestExpireAddons_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	now := time.Now().UTC()
	expired := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: uuid.New(),
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 300,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	addons.expiredList = []domain.QuotaAddon{*expired}
	_ = addons.Insert(context.Background(), expired)

	count, err := svc.ExpireAddons(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count) // expired ok, recompute failure is non-fatal
}

// ── ApplyBillingPurchase: BillingRefAlreadyClaimed ─────────────────────────

func TestApplyBillingPurchase_BillingRefAlreadyClaimed_ReturnedToCaller(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	// The billing_ref is held by another tenant's pack: Insert reports the
	// conflict (ON CONFLICT DO NOTHING) and this tenant has no such pack.
	addons.insertErr = domain.NewError(domain.ErrBillingRefAlreadyClaimed, "billing_ref already claimed")

	err := svc.ApplyBillingPurchase(context.Background(), port.ApplyBillingPurchaseRequest{
		TenantID: uuid.New(), Resource: "llm_tokens", Delta: 100_000,
		BillingRef: "inv_conflict", PurchasedAt: time.Now().UTC(),
	})
	// B-8: returned so the Billing consumer can count, alert and ack it.
	require.ErrorIs(t, err, domain.ErrBillingRefAlreadyClaimed)
}

func TestApplyBillingPurchase_InsertError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	addons.insertErr = errStorage

	err := svc.ApplyBillingPurchase(context.Background(), port.ApplyBillingPurchaseRequest{
		TenantID: uuid.New(), Resource: "llm_tokens", Delta: 100_000,
		BillingRef: "inv_insert_err", PurchasedAt: time.Now().UTC(),
	})
	require.Error(t, err)
}

// ── recordAddonAudit: with audit recorder in context ─────────────────────

func TestAddAddon_WithAuditRecorder_RecordsEntry(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	rec := &fakeAuditRecorder{}
	tx := &fakeTxRunner{pub: &fakePublisher{}, rec: rec}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner()).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	_, err := svc.AddAddon(context.Background(), port.AddAddonRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 1000, GrantedBy: uuid.New(), Reason: "audit test",
	})
	require.NoError(t, err)
	assert.Greater(t, rec.callCount(), 0)
}

func TestRemoveAddon_WithAuditRecorder_RecordsEntry(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	rec := &fakeAuditRecorder{}
	tx := &fakeTxRunner{pub: &fakePublisher{}, rec: rec}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner()).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

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
	assert.Greater(t, rec.callCount(), 0)
}

func TestExpireAddons_WithAuditRecorder_RecordsEntry(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	rec := &fakeAuditRecorder{}
	tx := &fakeTxRunner{pub: &fakePublisher{}, rec: rec}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner()).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	now := time.Now().UTC()
	expired := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: uuid.New(),
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 500, RemainingTokens: 300,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	addons.expiredList = []domain.QuotaAddon{*expired}
	_ = addons.Insert(context.Background(), expired)

	count, err := svc.ExpireAddons(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Greater(t, rec.callCount(), 0)
}

// ── RemoveAddon: update error ─────────────────────────────────────────────

func TestRemoveAddon_UpdateError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)
	addons.updateErr = errStorage

	err := svc.RemoveAddon(context.Background(), port.RemoveAddonRequest{
		TenantID: tenantID, AddonID: a.ID, RemovedBy: uuid.New(), Reason: "cleanup",
	})
	require.Error(t, err)
}

// ── RemoveOverride: InsertHistory error bubbles up ────────────────────────

func TestRemoveOverride_InsertHistoryError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	actor := uuid.New()
	_, _ = svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(10), Reason: "set", SetBy: actor,
	})
	overrides.deleteErr = errStorage

	err := svc.RemoveOverride(context.Background(), port.RemoveOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		RemovedBy: actor, Reason: "cleanup",
	})
	require.Error(t, err)
}

// ── SetOverride: recompute fails non-fatal ────────────────────────────────

func TestSetOverride_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})

	ov, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(10), Reason: "test", SetBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.NotNil(t, ov)
}

// ── ExpireOverrides: recompute fails non-fatal ─────────────────────────────

func TestExpireOverrides_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})

	past := time.Now().UTC().Add(-time.Hour)
	overrides.expiredList = []domain.Override{{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, Value: i64ptr(1),
		Reason: "expired", SetBy: uuid.New(), ExpiresAt: &past,
	}}
	_ = overrides.Upsert(context.Background(), &overrides.expiredList[0])

	count, err := svc.ExpireOverrides(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// ── computeChangedKeys: key in prev but not in new ─────────────────────────

func TestRecompute_ComputeChangedKeys_RemovedKey(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()
	// Pre-load snapshot with an extra custom key not generated by BaseLimits
	extraKey := domain.LimitKey("custom_dimension")
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly:    {Value: i64ptr(1_000_000)},
		domain.LimitKeyAPIRequestBudgetMonthly: {Value: i64ptr(50_000)},
		domain.LimitKeyAPIRateLimitRPS:         {Value: i64ptr(100)},
		domain.LimitKeyWorkflowTemplateLimit:   {Value: nil},
		domain.LimitKeyTenderLimit:             {Value: nil},
		extraKey:                               {Value: i64ptr(999)},
	}
	_, _ = snaps.Upsert(context.Background(), &domain.EntitlementSnapshot{
		TenantID:  tenantID,
		Version:   1,
		Plan:      "starter",
		State:     domain.EntitlementStateActive,
		ModelTier: "standard",
		Limits:    limits,
	})

	svc := service.NewEffectiveSetService(
		snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(),
		&fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner(),
	).WithLogger(port.NopLogger{})

	// Recompute: new limits from plan have 5 keys; extraKey is in prev but not in new
	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.NotNil(t, es)
	// extraKey is NOT in the new effective set (BaseLimits only produces 5 keys)
	_, hasExtra := es.Limits[extraKey]
	assert.False(t, hasExtra)
}

// ── effectiveSetUnchanged: unlimited vs bounded mismatch ──────────────────

func TestRecompute_EffectiveSetUnchanged_UnlimitedMismatch(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()

	callCount := 0
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(t, "starter", "active"), nil
		},
	}
	// First call: plan has bounded LLM quota
	plan1 := samplePlan("starter") // LLMTokenQuotaMonthly = 1_000_000
	// Second call: plan has unlimited LLM quota
	plan2 := samplePlan("starter")
	plan2.LLMTokenQuotaMonthly = nil // unlimited

	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) {
			callCount++
			if callCount == 1 {
				return plan1, nil
			}
			return plan2, nil
		},
	}

	svc := service.NewEffectiveSetService(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), catalog, iam, nil, newTxRunner()).WithLogger(port.NopLogger{})

	// First recompute: bounded quota
	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)

	// Second recompute: unlimited quota (IsUnlimited mismatch triggers effectiveSetUnchanged=false)
	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.True(t, es.Limits[domain.LimitKeyLLMTokenQuotaMonthly].IsUnlimited())
}

// ── RecordDrawdown: drawdown insert error ─────────────────────────────────

func TestRecordDrawdown_DrawdownInsertError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	drawdowns := newDrawdownRepo()
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, drawdowns, shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 10_000, RemainingTokens: 10_000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)
	drawdowns.insertErr = errStorage

	_, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 100, PeriodStart: now,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert drawdown")
}

func TestRecordDrawdown_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	d, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 100, PeriodStart: now,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(100), d.TokensConsumed)
}

// ── AssignTrialProfile: recompute fails non-fatal ─────────────────────────

func TestAssignTrialProfile_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})

	now := time.Now().UTC()
	a, err := svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		TenantID: uuid.New(), PlanCode: "starter", Profile: domain.TrialProfileFullPlan,
		StartsAt: now, EndsAt: now.Add(24 * time.Hour), SetBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.NotNil(t, a)
}

// ── AddAddon: recompute fails non-fatal ───────────────────────────────────

func TestAddAddon_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	a, err := svc.AddAddon(context.Background(), port.AddAddonRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens: 1000, GrantedBy: uuid.New(), Reason: "test",
	})
	require.NoError(t, err)
	assert.NotNil(t, a)
}

// ── RemoveAddon: recompute fails non-fatal ────────────────────────────────

func TestRemoveAddon_RecomputeFailsNonFatal(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.RemoveAddon(context.Background(), port.RemoveAddonRequest{
		TenantID: tenantID, AddonID: a.ID, RemovedBy: uuid.New(), Reason: "test",
	})
	require.NoError(t, err)
}

// ── ApplyBillingRefund: recompute fails propagates ────────────────────────

func TestApplyBillingRefund_RecomputeFailsPropagates(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{}).
		WithRefundRepository(newRefundRepo())

	tenantID := uuid.New()
	purchaseRef := uuid.New()
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		BillingRef: strptr(purchaseRef.String()),
		Status:     domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), a)

	err := svc.ApplyBillingRefund(context.Background(), port.ApplyBillingRefundRequest{
		TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -100, BillingRef: uuid.New(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recompute failed")
}

// ── ApplyBillingPurchase: recompute fails propagates ──────────────────────

func TestApplyBillingPurchase_RecomputeFailsPropagates(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	err := svc.ApplyBillingPurchase(context.Background(), port.ApplyBillingPurchaseRequest{
		TenantID: uuid.New(), Resource: "llm_tokens", Delta: 100_000,
		BillingRef: "inv_recompute_fail", PurchasedAt: time.Now().UTC(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recompute failed")
}

// ── ResolveShortfall: update error propagates ─────────────────────────────

func TestResolveShortfall_UpdateError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, newDrawdownRepo(), shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})

	sfID := uuid.New()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: uuid.New(), Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	shortfalls.updateErr = errStorage

	_, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
		ShortfallID: sfID, Disposition: domain.DispositionReject, DispositionBy: uuid.New(),
	})
	require.Error(t, err)
}

// ── port/context.go: NopLogger Info/Warn/Error/Debug are empty ────────────
// These single-line no-op functions have no coverable statements in Go
// coverage, but we call them here to show they don't panic.

func TestNopLogger_AllMethods_NoPanic(t *testing.T) {
	var l port.NopLogger
	fields := map[string]any{"k": "v"}
	l.Debug("d", fields)
	l.Info("i", fields)
	l.Warn("w", fields)
	l.Error("e", fields)
}

// ── ExpireAddons: list expired error propagates ───────────────────────────

func TestExpireAddons_ListExpiredError_PropagatesError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	addons.listExpiredErr = errors.New("list expired failed")
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})

	_, err := svc.ExpireAddons(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list expired addons")
}

// ── RecordDrawdown: addon update error in tx ──────────────────────────────

func TestRecordDrawdown_AddonUpdateError_PropagatesTxError(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner()).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	now := time.Now().UTC()
	addon := &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey: domain.LimitKeyLLMTokenQuotaMonthly, TotalTokens: 1000, RemainingTokens: 1000,
		Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	_ = addons.Insert(context.Background(), addon)
	addons.updateErr = errors.New("addon update failed")

	svc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})
	_, err := svc.RecordDrawdown(context.Background(), port.RecordDrawdownRequest{
		TenantID:       tenantID,
		LimitKey:       domain.LimitKeyLLMTokenQuotaMonthly,
		TokensConsumed: 500,
		PeriodStart:    now,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update addon")
}

// ── ResolveShortfall: recompute failure is a warning (non-fatal) ──────────

func TestResolveShortfall_RecomputeFailsNonFatal_Approve(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	// Use a catalog service that fails so recompute fails.
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) {
			return nil, errors.New("catalog down")
		},
	}
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, catalog, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, newDrawdownRepo(), shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})

	sfID := uuid.New()
	now := time.Now().UTC()
	shortfalls.shortfalls[sfID] = &domain.QuotaAddonShortfall{
		ID: sfID, TenantID: uuid.New(), Status: domain.ShortfallStatusPendingApproval,
		CreatedAt: now, UpdatedAt: now,
	}

	dispRef := "ref-approval-123"
	// Approve disposition triggers recompute which fails → warning logged, success returned.
	sf, err := svc.ResolveShortfall(context.Background(), port.ResolveShortfallRequest{
		ShortfallID: sfID, Disposition: domain.DispositionApprove, DispositionBy: uuid.New(),
		DispositionRef: &dispRef,
	})
	require.NoError(t, err, "recompute failure must not propagate to caller")
	assert.NotNil(t, sf)
}

// ── RemoveOverride: recompute failure logs warning (non-fatal) ───────────

func TestRemoveOverride_RecomputeFailsNonFatal(t *testing.T) {
	overrides := newOverrideRepo()
	tx := newTxRunner()
	failIAM := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	effectSvc := service.NewEffectiveSetService(newSnapshotRepo(), overrides, newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, failIAM, nil, tx)
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	val := int64(100)
	now := time.Now().UTC()
	_ = overrides.Upsert(context.Background(), &domain.Override{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit, Value: &val,
		Reason: "test", SetBy: uuid.New(), RecordVersion: 0, CreatedAt: now, UpdatedAt: now,
	})

	err := svc.RemoveOverride(context.Background(), port.RemoveOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		RemovedBy: uuid.New(), Reason: "cleanup",
	})
	require.NoError(t, err, "recompute failure after RemoveOverride must not propagate")
}

// ── ExpireOverrides: list expired error propagates ───────────────────────

func TestExpireOverrides_ListExpiredError(t *testing.T) {
	overrides := newOverrideRepo()
	overrides.listExpiredErr = errors.New("list expired overrides failed")
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(newSnapshotRepo(), overrides, newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx)
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})

	_, err := svc.ExpireOverrides(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list expired overrides")
}
