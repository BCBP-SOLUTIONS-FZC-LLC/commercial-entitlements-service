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

// ── builder helpers ────────────────────────────────────────────────────────

func newEffectSvc(snaps port.SnapshotRepository, overrides port.OverrideRepository, addons port.AddonRepository, trials port.TrialRepository, catalog port.CatalogService, iam port.IAMClient, cache port.Cache, tx port.TxRunner) *service.EffectiveSetService {
	return service.NewEffectiveSetService(snaps, overrides, addons, trials, catalog, iam, cache, tx)
}

func defaultEffectSvc() (*service.EffectiveSetService, *fakeSnapshotRepo, *fakeOverrideRepo, *fakeAddonRepo, *fakeTrialRepo, *fakeTxRunner) { //nolint:unparam
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	iam := &fakeIAMClient{}
	catalog := &fakeCatalogSvc{}
	svc := newEffectSvc(snaps, overrides, addons, trials, catalog, iam, nil, tx).WithLogger(port.NopLogger{})
	return svc, snaps, overrides, addons, trials, tx
}

// ── GetEffectiveSet ────────────────────────────────────────────────────────

func TestGetEffectiveSet_CacheHit(t *testing.T) {
	tenantID := uuid.New()
	cached := &domain.EffectiveSet{TenantID: tenantID, PlanCode: "pro", Version: 5}
	cache := newFakeCache()
	_ = cache.SetEffectiveSet(context.Background(), tenantID, cached)

	snaps := newSnapshotRepo()
	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, cache, newTxRunner())

	es, err := svc.GetEffectiveSet(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "pro", es.PlanCode)
	assert.Equal(t, int64(5), es.Version)
}

func TestGetEffectiveSet_CacheMiss_SnapFallback(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()
	_, _ = snaps.Upsert(context.Background(), &domain.EntitlementSnapshot{
		TenantID: tenantID,
		Version:  2,
		Plan:     "starter",
		State:    domain.EntitlementStateActive,
		Limits:   map[domain.LimitKey]domain.LimitValue{},
	})
	cache := newFakeCache() // empty cache → miss

	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, cache, newTxRunner())

	es, err := svc.GetEffectiveSet(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "starter", es.PlanCode)
	assert.Equal(t, int64(2), es.Version)
}

func TestGetEffectiveSet_NilCache_SnapFallback(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()
	_, _ = snaps.Upsert(context.Background(), &domain.EntitlementSnapshot{
		TenantID: tenantID, Version: 1, Plan: "basic", State: domain.EntitlementStateActive,
		Limits: map[domain.LimitKey]domain.LimitValue{},
	})

	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner())
	es, err := svc.GetEffectiveSet(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "basic", es.PlanCode)
}

func TestGetEffectiveSet_SnapshotNotFound(t *testing.T) {
	tenantID := uuid.New()
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner())

	_, err := svc.GetEffectiveSet(context.Background(), tenantID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrSnapshotNotFound))
}

func TestGetEffectiveSet_SnapRepoError(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()
	snaps.getErr = errStorage

	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner())
	_, err := svc.GetEffectiveSet(context.Background(), tenantID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get snapshot")
}

func TestGetEffectiveSet_CacheError_FallsBackToSnap(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()
	_, _ = snaps.Upsert(context.Background(), &domain.EntitlementSnapshot{
		TenantID: tenantID, Version: 1, Plan: "pro", State: domain.EntitlementStateActive,
		Limits: map[domain.LimitKey]domain.LimitValue{},
	})
	cache := newFakeCache()
	cache.getEffSetFn = func(_ context.Context, _ uuid.UUID) (*domain.EffectiveSet, error) {
		return nil, errors.New("cache error")
	}

	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, cache, newTxRunner())
	es, err := svc.GetEffectiveSet(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "pro", es.PlanCode)
}

// ── Recompute – basic paths ────────────────────────────────────────────────

func TestRecompute_HappyPath_ActiveState(t *testing.T) {
	tenantID := uuid.New()
	svc, snaps, _, _, _, tx := defaultEffectSvc()

	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.Equal(t, tenantID, es.TenantID)
	assert.Equal(t, domain.EntitlementStateActive, es.State)
	assert.Equal(t, int64(1), es.Version)

	// event emitted
	assert.Contains(t, tx.pub.eventTypes(), domain.EventTypeTenantEntitlementsChanged)

	// snapshot persisted
	snap, err := snaps.GetLatest(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), snap.Version)
}

func TestRecompute_IAMClientError(t *testing.T) {
	tenantID := uuid.New()
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, _ uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam unavailable")
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, iam, nil, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch subscription")
}

func TestRecompute_CatalogError(t *testing.T) {
	tenantID := uuid.New()
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) {
			return nil, errors.New("catalog down")
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), catalog, &fakeIAMClient{}, nil, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get plan")
}

func TestRecompute_TxError(t *testing.T) {
	tenantID := uuid.New()
	tx := &fakeTxRunner{err: errStorage}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx)

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
}

func TestRecompute_UpsertError(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()
	snaps.upsertErr = errStorage
	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
}

// ── ENT-INV-6: noop guard ──────────────────────────────────────────────────

func TestRecompute_ENT_INV6_Noop_SameSet(t *testing.T) {
	tenantID := uuid.New()
	svc, snaps, _, _, _, tx := defaultEffectSvc()

	// First recompute writes snapshot
	es1, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.Equal(t, int64(1), es1.Version)
	firstEventCount := len(tx.pub.events)

	// Second recompute with same plan/state → ENT-INV-6 noop
	es2, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	// No new events emitted
	assert.Equal(t, firstEventCount, len(tx.pub.events))
	_ = snaps
	_ = es2
}

// ── ENT-INV-6: concurrent write (written=false) ────────────────────────────

func TestRecompute_ConcurrentWrite_WrittenFalse(t *testing.T) {
	tenantID := uuid.New()
	snaps := newSnapshotRepo()
	// Pre-load snapshot with version 99 so the upsert sees incoming version < stored
	_, _ = snaps.Upsert(context.Background(), &domain.EntitlementSnapshot{
		TenantID:  tenantID,
		Version:   99,
		Plan:      "starter",
		State:     domain.EntitlementStateActive,
		Limits:    map[domain.LimitKey]domain.LimitValue{domain.LimitKeyAPIRateLimitRPS: {Value: i64ptr(100)}},
		ModelTier: "standard",
	})
	// Override upsert so it always returns written=false
	snaps.upsertFn = func(_ *domain.EntitlementSnapshot) (bool, error) {
		return false, nil
	}
	tx := newTxRunner()
	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx)

	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.NotNil(t, es)
	// No event should be in tx.pub because written=false
	assert.Empty(t, tx.pub.events)
}

// ── Recompute hook ─────────────────────────────────────────────────────────

func TestRecompute_HookFiredOnSuccess(t *testing.T) {
	tenantID := uuid.New()
	svc, _, _, _, _, _ := defaultEffectSvc()

	var hookCause domain.EffectiveSetCause
	var hookNoop, hookFailed bool
	var hookDuration time.Duration
	svc.WithRecomputeHook(func(cause domain.EffectiveSetCause, noop, failed bool, d time.Duration) {
		hookCause = cause
		hookNoop = noop
		hookFailed = failed
		hookDuration = d
	})

	_, err := svc.Recompute(context.Background(), tenantID, domain.CauseAddonChange)
	require.NoError(t, err)
	assert.Equal(t, domain.CauseAddonChange, hookCause)
	assert.False(t, hookFailed)
	assert.False(t, hookNoop)
	assert.True(t, hookDuration >= 0)
}

func TestRecompute_HookFiredOnNoop(t *testing.T) {
	tenantID := uuid.New()
	svc, _, _, _, _, _ := defaultEffectSvc()

	var hookNoop bool
	svc.WithRecomputeHook(func(_ domain.EffectiveSetCause, noop, _ bool, _ time.Duration) {
		hookNoop = noop
	})

	// First recompute
	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.False(t, hookNoop)

	// Second identical recompute → noop
	_, err = svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.True(t, hookNoop)
}

func TestRecompute_HookFiredOnFailure(t *testing.T) {
	tenantID := uuid.New()
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, _ uuid.UUID) (*port.TenantSubscription, error) {
			return nil, errors.New("iam down")
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, iam, nil, newTxRunner())

	var hookFailed bool
	svc.WithRecomputeHook(func(_ domain.EffectiveSetCause, _, failed bool, _ time.Duration) {
		hookFailed = failed
	})

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
	assert.True(t, hookFailed)
}

// ── Trial state: profile limits ────────────────────────────────────────────

func TestRecompute_TrialState_ProfileLimitsApplied(t *testing.T) {
	tenantID := uuid.New()
	trialLLM := int64(100_000)
	plan := samplePlan("starter")
	plan.TrialProfiles = map[string]domain.TrialProfileLimits{
		string(domain.TrialProfileReducedPlan): {
			LLMTokenQuotaMonthly: &trialLLM,
		},
	}

	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(t, "starter", "trial"), nil
		},
		fetchPlanFn: func(_ context.Context, code string) (*domain.Plan, error) {
			return plan, nil
		},
	}
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}

	trials := newTrialRepo()
	now := time.Now().UTC()
	_ = trials.UpsertAssignment(context.Background(), &domain.TenantTrialAssignment{
		TenantID:      tenantID,
		PlanCode:      "starter",
		Profile:       domain.TrialProfileReducedPlan,
		StartsAt:      now,
		EndsAt:        now.Add(30 * 24 * time.Hour),
		RecordVersion: 1,
	})

	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials, catalog, iam, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	assert.Equal(t, domain.EntitlementStateTrial, es.State)
	assert.Equal(t, trialLLM, *es.Limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
	assert.Equal(t, string(domain.TrialProfileReducedPlan), *es.TrialProfile)
}

func TestRecompute_TrialState_WithTrialOverrides(t *testing.T) {
	tenantID := uuid.New()
	plan := samplePlan("starter")
	plan.TrialProfiles = map[string]domain.TrialProfileLimits{}

	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(t, "starter", "trial"), nil
		},
	}
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}

	trials := newTrialRepo()
	now := time.Now().UTC()
	_ = trials.UpsertAssignment(context.Background(), &domain.TenantTrialAssignment{
		TenantID: tenantID, PlanCode: "starter",
		Profile:  domain.TrialProfileCustom,
		StartsAt: now, EndsAt: now.Add(30 * 24 * time.Hour),
		RecordVersion: 1,
	})
	trialOvVal := int64(99)
	_ = trials.UpsertOverride(context.Background(), &domain.TrialEntitlementOverride{
		TenantID: tenantID,
		LimitKey: domain.LimitKeyAPIRateLimitRPS,
		Value:    &trialOvVal,
	})

	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials, catalog, iam, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	assert.Equal(t, trialOvVal, *es.Limits[domain.LimitKeyAPIRateLimitRPS].Value)
}

// ── Operator overrides ─────────────────────────────────────────────────────

func TestRecompute_OperatorOverridesApplied(t *testing.T) {
	tenantID := uuid.New()
	overrides := newOverrideRepo()
	ovVal := int64(500)
	_ = overrides.Upsert(context.Background(), &domain.Override{
		TenantID: tenantID,
		LimitKey: domain.LimitKeyTenderLimit,
		Value:    &ovVal,
		Reason:   "test",
		SetBy:    uuid.New(),
	})

	iam := &fakeIAMClient{}
	catalog := &fakeCatalogSvc{}
	svc2 := newEffectSvc(newSnapshotRepo(), overrides, newAddonRepo(), newTrialRepo(), catalog, iam, nil, newTxRunner())
	es, err := svc2.Recompute(context.Background(), tenantID, domain.CauseOverrideChange)
	require.NoError(t, err)
	assert.Equal(t, ovVal, *es.Limits[domain.LimitKeyTenderLimit].Value)
}

func TestRecompute_ExpiredOperatorOverridesSkipped(t *testing.T) {
	tenantID := uuid.New()
	overrides := newOverrideRepo()
	past := time.Now().UTC().Add(-time.Hour)
	ovVal := int64(1)
	_ = overrides.Upsert(context.Background(), &domain.Override{
		TenantID:  tenantID,
		LimitKey:  domain.LimitKeyTenderLimit,
		Value:     &ovVal,
		Reason:    "test",
		SetBy:     uuid.New(),
		ExpiresAt: &past,
	})

	plan := samplePlan("starter")
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}
	svc := newEffectSvc(newSnapshotRepo(), overrides, newAddonRepo(), newTrialRepo(), catalog, &fakeIAMClient{}, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseOverrideChange)
	require.NoError(t, err)
	// Tender limit should be from plan baseline (nil → unlimited), not the expired override
	assert.True(t, es.Limits[domain.LimitKeyTenderLimit].IsUnlimited())
}

// ── Addon quota balance ────────────────────────────────────────────────────

func TestRecompute_AddonBalance_AddsToLimit(t *testing.T) {
	tenantID := uuid.New()
	addons := newAddonRepo()
	// Insert an active addon with 50k tokens
	now := time.Now().UTC()
	_ = addons.Insert(context.Background(), &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey:        domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens:     50_000,
		RemainingTokens: 50_000,
		Status:          domain.AddonStatusActive,
		CreatedAt:       now, UpdatedAt: now,
		RecordVersion: 1,
	})

	plan := samplePlan("starter") // plan has 1_000_000 LLM tokens
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), addons, newTrialRepo(), catalog, &fakeIAMClient{}, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseAddonChange)
	require.NoError(t, err)
	assert.Equal(t, int64(1_050_000), *es.Limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
}

func TestRecompute_AddonBalance_IgnoredWhenPlanUnlimited(t *testing.T) {
	tenantID := uuid.New()
	addons := newAddonRepo()
	now := time.Now().UTC()
	_ = addons.Insert(context.Background(), &domain.QuotaAddon{
		ID: uuid.New(), TenantID: tenantID,
		LimitKey:        domain.LimitKeyLLMTokenQuotaMonthly,
		TotalTokens:     50_000,
		RemainingTokens: 50_000,
		Status:          domain.AddonStatusActive,
		CreatedAt:       now, UpdatedAt: now,
		RecordVersion: 1,
	})

	plan := samplePlan("enterprise")
	plan.LLMTokenQuotaMonthly = nil // unlimited
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), addons, newTrialRepo(), catalog, &fakeIAMClient{}, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseAddonChange)
	require.NoError(t, err)
	assert.True(t, es.Limits[domain.LimitKeyLLMTokenQuotaMonthly].IsUnlimited())
}

// ── Cache write (ENT-D13) ──────────────────────────────────────────────────

func TestRecompute_WritesQuotaCeilingToCache(t *testing.T) {
	tenantID := uuid.New()
	cache := newFakeCache()
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, cache, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)

	ceilKey := tenantID.String() + ":" + string(domain.LimitKeyLLMTokenQuotaMonthly)
	assert.NotNil(t, cache.ceilings[ceilKey])
}

func TestRecompute_CacheWrite_QuotaCeilingError_IsNonFatal(t *testing.T) {
	tenantID := uuid.New()
	cache := newFakeCache()
	cache.setQuotaCeilErr = errors.New("valkey down")
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, cache, newTxRunner()).WithLogger(port.NopLogger{})

	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.NotNil(t, es)
}

func TestRecompute_CacheWarmsEffectiveSet(t *testing.T) {
	tenantID := uuid.New()
	cache := newFakeCache()
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, cache, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)

	cached, _ := cache.GetEffectiveSet(context.Background(), tenantID)
	assert.NotNil(t, cached)
}

// ── All subscription state mappings ───────────────────────────────────────

func TestRecompute_AllSubscriptionStates(t *testing.T) {
	states := []struct {
		input    string
		expected domain.EntitlementState
	}{
		{"active", domain.EntitlementStateActive},
		{"trial", domain.EntitlementStateTrial},
		{"grace_period", domain.EntitlementStateGracePeriod},
		{"suspended", domain.EntitlementStateSuspended},
		{"offboarded", domain.EntitlementStateOffboarded},
		{"provisioning", domain.EntitlementStateProvisioning},
		{"unknown_state", domain.EntitlementStateSuspended},
		{"", domain.EntitlementStateSuspended},
	}
	for _, tc := range states {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			tenantID := uuid.New()
			iam := &fakeIAMClient{
				fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
					return sampleSub(t, "starter", tc.input), nil
				},
			}
			svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, iam, nil, newTxRunner())
			es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, es.State)
		})
	}
}

// ── AddonEligibility ───────────────────────────────────────────────────────

func TestRecompute_AddonEligibility_ActiveBoundedQuota(t *testing.T) {
	tenantID := uuid.New()
	plan := samplePlan("starter") // has bounded LLM quota
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), catalog, &fakeIAMClient{}, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.True(t, es.AddonEligibility["llm_tokens"].Eligible)
	assert.Nil(t, es.AddonEligibility["llm_tokens"].Reason)
}

func TestRecompute_AddonEligibility_ActiveUnlimitedQuota(t *testing.T) {
	tenantID := uuid.New()
	plan := samplePlan("enterprise")
	plan.LLMTokenQuotaMonthly = nil // unlimited
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), catalog, &fakeIAMClient{}, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.False(t, es.AddonEligibility["llm_tokens"].Eligible)
	assert.Equal(t, "unlimited_quota", *es.AddonEligibility["llm_tokens"].Reason)
}

func TestRecompute_AddonEligibility_TrialState(t *testing.T) {
	tenantID := uuid.New()
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(t, "starter", "trial"), nil
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, iam, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	// HLD v0.69 P-5 (E-10): a trial may buy token packs, not API-request packs.
	assert.True(t, es.AddonEligibility["llm_tokens"].Eligible)
	assert.Nil(t, es.AddonEligibility["llm_tokens"].Reason)
	assert.False(t, es.AddonEligibility["api_requests"].Eligible)
	assert.Equal(t, "trial", *es.AddonEligibility["api_requests"].Reason)
}

func TestRecompute_AddonEligibility_SuspendedState(t *testing.T) {
	tenantID := uuid.New()
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(t, "starter", "suspended"), nil
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, iam, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.False(t, es.AddonEligibility["llm_tokens"].Eligible)
	assert.Equal(t, "not_active", *es.AddonEligibility["llm_tokens"].Reason)
}

// ── WithLogger / WithRecomputeHook ─────────────────────────────────────────

func TestEffectiveSetService_WithLogger(t *testing.T) {
	svc, _, _, _, _, _ := defaultEffectSvc()
	svc.WithLogger(port.NopLogger{})
	// No assertion; just exercises the setter path without panic
}

// ── Version increments on second recompute ─────────────────────────────────

func TestRecompute_VersionIncrementsOnChange(t *testing.T) {
	tenantID := uuid.New()
	plan1 := samplePlan("starter")
	plan2 := samplePlan("pro")
	plan2.LLMTokenQuotaMonthly = i64ptr(5_000_000) // different limit

	callCount := 0
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			callCount++
			if callCount == 1 {
				return sampleSub(t, "starter", "active"), nil
			}
			return sampleSub(t, "pro", "active"), nil
		},
	}
	getCalls := 0
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, code string) (*domain.Plan, error) {
			getCalls++
			if getCalls == 1 {
				return plan1, nil
			}
			return plan2, nil
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), catalog, iam, nil, newTxRunner())

	es1, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.Equal(t, int64(1), es1.Version)

	es2, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.Equal(t, int64(2), es2.Version)
}

// ── Event publisher not in context ────────────────────────────────────────

func TestRecompute_NoPublisher_InContext_StillWrites(t *testing.T) {
	tenantID := uuid.New()
	// Use a tx runner that does NOT inject a publisher
	tx := &fakeTxRunner{pub: nil}
	tx.pub = nil
	snaps := newSnapshotRepo()
	svc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx)

	es, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.NoError(t, err)
	assert.NotNil(t, es)
}

// ── applyTrialProfileLimits full field coverage ────────────────────────────

func TestRecompute_TrialProfile_AllLimitFields(t *testing.T) {
	tenantID := uuid.New()
	rps := 25
	wf := 5
	tender := 10
	trialLLM := int64(200_000)
	trialAPI := int64(2_000)
	plan := samplePlan("starter")
	// custom draws its base from the trial_specific entry (CD-51; Catalog has no custom entry).
	plan.TrialProfiles = map[string]domain.TrialProfileLimits{
		string(domain.TrialProfileTrialSpecific): {
			LLMTokenQuotaMonthly:    &trialLLM,
			APIRequestBudgetMonthly: &trialAPI,
			APIRateLimitRPS:         &rps,
			WorkflowTemplateLimit:   &wf,
			TenderLimit:             &tender,
		},
	}

	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, t uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(t, "starter", "trial"), nil
		},
	}
	catalog := &fakeCatalogSvc{
		getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil },
	}
	now := time.Now().UTC()
	trials := newTrialRepo()
	_ = trials.UpsertAssignment(context.Background(), &domain.TenantTrialAssignment{
		TenantID: tenantID, PlanCode: "starter",
		Profile:  domain.TrialProfileCustom,
		StartsAt: now, EndsAt: now.Add(30 * 24 * time.Hour),
		RecordVersion: 1,
	})

	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials, catalog, iam, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	assert.Equal(t, trialLLM, *es.Limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
	assert.Equal(t, trialAPI, *es.Limits[domain.LimitKeyAPIRequestBudgetMonthly].Value)
	assert.Equal(t, int64(rps), *es.Limits[domain.LimitKeyAPIRateLimitRPS].Value)
	assert.Equal(t, int64(wf), *es.Limits[domain.LimitKeyWorkflowTemplateLimit].Value)
	assert.Equal(t, int64(tender), *es.Limits[domain.LimitKeyTenderLimit].Value)
}

// ── Error path coverage for recomputeCore ─────────────────────────────────

func TestRecompute_UnexpectedTrialAssignmentError_PropagatesError(t *testing.T) {
	tenantID := uuid.New()
	trials := newTrialRepo()
	trials.getAssignErr = errors.New("db connection failed")
	// Use a trial subscription so Step 2 (GetTrialAssignment) is reached.
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, id uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(id, "starter", "trial"), nil
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials, &fakeCatalogSvc{}, iam, nil, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get trial assignment")
}

func TestRecompute_OperatorOverrideListError_PropagatesError(t *testing.T) {
	tenantID := uuid.New()
	overrides := newOverrideRepo()
	overrides.listErr = errors.New("db connection failed")
	svc := newEffectSvc(newSnapshotRepo(), overrides, newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list operator overrides")
}

func TestRecompute_AddonSumError_PropagatesError(t *testing.T) {
	tenantID := uuid.New()
	addons := newAddonRepo()
	addons.sumFn = func(_ uuid.UUID, _ domain.LimitKey) (int64, error) {
		return 0, errors.New("db connection failed")
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), addons, newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sum addon balance")
}

func TestRecompute_PublisherEnqueueError_PropagatesTxError(t *testing.T) {
	tenantID := uuid.New()
	tx := newTxRunner()
	tx.pub.err = errors.New("publisher failed")
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx)

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
}

func TestRecompute_TrialOverrideListError_PropagatesError(t *testing.T) {
	tenantID := uuid.New()
	trials := newTrialRepo()
	trials.listOvErr = errors.New("list trial overrides failed")
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, id uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(id, "starter", "trial"), nil
		},
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials, &fakeCatalogSvc{}, iam, nil, newTxRunner())

	_, err := svc.Recompute(context.Background(), tenantID, domain.CausePlanChange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list trial overrides")
}
