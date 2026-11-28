// Package service — white-box tests for unexported helpers.
// These cannot live in test/unit/ because the symbols are package-private.
package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── min64 ─────────────────────────────────────────────────────────────────

func TestMin64(t *testing.T) {
	assert.Equal(t, int64(3), min64(3, 5))
	assert.Equal(t, int64(3), min64(5, 3))
	assert.Equal(t, int64(0), min64(0, 100))
	assert.Equal(t, int64(-1), min64(-1, 0))
}

// ── addonAuditKey ─────────────────────────────────────────────────────────

func TestAddonAuditKey_Deterministic(t *testing.T) {
	id := uuid.New()
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k1 := addonAuditKey(id, 1, ts)
	k2 := addonAuditKey(id, 1, ts)
	assert.Equal(t, k1, k2, "same inputs must produce same key")
}

func TestAddonAuditKey_DifferentVersion_DifferentKey(t *testing.T) {
	id := uuid.New()
	ts := time.Now().UTC()
	assert.NotEqual(t, addonAuditKey(id, 1, ts), addonAuditKey(id, 2, ts))
}

// ── validateLimitKey ──────────────────────────────────────────────────────

func TestValidateLimitKey_KnownKeys(t *testing.T) {
	for _, k := range []domain.LimitKey{
		domain.LimitKeyLLMTokenQuotaMonthly,
		domain.LimitKeyAPIRequestBudgetMonthly,
		domain.LimitKeyAPIRateLimitRPS,
		domain.LimitKeyWorkflowTemplateLimit,
		domain.LimitKeyTenderLimit,
	} {
		assert.NoError(t, validateLimitKey(k), "key %s should be valid", k)
	}
}

func TestValidateLimitKey_UnknownKey(t *testing.T) {
	for _, k := range []domain.LimitKey{"", "unknown", "storage_gb"} {
		err := validateLimitKey(k)
		assert.ErrorIs(t, err, domain.ErrInvalidLimitKey, "key %q should be invalid", k)
	}
}

// ── mapSubscriptionState ──────────────────────────────────────────────────

func TestMapSubscriptionState(t *testing.T) {
	cases := []struct {
		input    string
		expected domain.EntitlementState
	}{
		{"active", domain.EntitlementStateActive},
		{"trial", domain.EntitlementStateTrial},
		{"grace_period", domain.EntitlementStateGracePeriod},
		{"suspended", domain.EntitlementStateSuspended},
		{"offboarded", domain.EntitlementStateOffboarded},
		{"provisioning", domain.EntitlementStateProvisioning},
		// iam-org-membership billing-delinquency states → grace_period
		{"past_due", domain.EntitlementStateGracePeriod},
		{"cancelled", domain.EntitlementStateGracePeriod},
		// iam-org-membership trial that elapsed → suspended
		{"trial_expired", domain.EntitlementStateSuspended},
		{"unknown", domain.EntitlementStateSuspended},
		{"", domain.EntitlementStateSuspended},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, mapSubscriptionState(tc.input), "state %q", tc.input)
	}
}

// ── limitValEqual ─────────────────────────────────────────────────────────

func TestLimitValEqual(t *testing.T) {
	v100 := int64(100)
	v200 := int64(200)
	assert.True(t, limitValEqual(domain.LimitValue{}, domain.LimitValue{}))                          // both unlimited
	assert.False(t, limitValEqual(domain.LimitValue{}, domain.LimitValue{Value: &v100}))             // unlimited vs bounded
	assert.False(t, limitValEqual(domain.LimitValue{Value: &v100}, domain.LimitValue{}))             // bounded vs unlimited
	assert.True(t, limitValEqual(domain.LimitValue{Value: &v100}, domain.LimitValue{Value: &v100}))  // same value
	assert.False(t, limitValEqual(domain.LimitValue{Value: &v100}, domain.LimitValue{Value: &v200})) // different value
}

// ── effectiveSetUnchanged ─────────────────────────────────────────────────

func TestEffectiveSetUnchanged_SameSets(t *testing.T) {
	v := int64(1000)
	es := &domain.EffectiveSet{
		PlanCode:     "starter",
		State:        domain.EntitlementStateActive,
		ModelTier:    "standard",
		FeatureFlags: []string{"sso_enabled"},
		Limits:       map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v}},
	}
	snap := &domain.EntitlementSnapshot{
		Plan:         "starter",
		State:        domain.EntitlementStateActive,
		ModelTier:    "standard",
		FeatureFlags: []string{"sso_enabled"},
		Limits:       map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v}},
	}
	assert.True(t, effectiveSetUnchanged(es, snap))
}

func TestEffectiveSetUnchanged_DifferentFeatureFlags(t *testing.T) {
	es := &domain.EffectiveSet{
		PlanCode: "pro", State: domain.EntitlementStateActive, ModelTier: "standard",
		FeatureFlags: []string{"custom_branding", "sso_enabled"},
		Limits:       map[domain.LimitKey]domain.LimitValue{},
	}
	snap := &domain.EntitlementSnapshot{
		Plan: "pro", State: domain.EntitlementStateActive, ModelTier: "standard",
		FeatureFlags: []string{"custom_branding"},
		Limits:       map[domain.LimitKey]domain.LimitValue{},
	}
	assert.False(t, effectiveSetUnchanged(es, snap))
}

func TestEffectiveSetUnchanged_DifferentPlan(t *testing.T) {
	es := &domain.EffectiveSet{PlanCode: "pro", State: domain.EntitlementStateActive, ModelTier: "standard", Limits: map[domain.LimitKey]domain.LimitValue{}}
	snap := &domain.EntitlementSnapshot{Plan: "starter", State: domain.EntitlementStateActive, ModelTier: "standard", Limits: map[domain.LimitKey]domain.LimitValue{}}
	assert.False(t, effectiveSetUnchanged(es, snap))
}

func TestEffectiveSetUnchanged_DifferentState(t *testing.T) {
	es := &domain.EffectiveSet{PlanCode: "s", State: domain.EntitlementStateTrial, ModelTier: "s", Limits: map[domain.LimitKey]domain.LimitValue{}}
	snap := &domain.EntitlementSnapshot{Plan: "s", State: domain.EntitlementStateActive, ModelTier: "s", Limits: map[domain.LimitKey]domain.LimitValue{}}
	assert.False(t, effectiveSetUnchanged(es, snap))
}

func TestEffectiveSetUnchanged_DifferentLimitCount(t *testing.T) {
	v := int64(1)
	es := &domain.EffectiveSet{
		PlanCode: "s", State: domain.EntitlementStateActive, ModelTier: "s",
		Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v}},
	}
	snap := &domain.EntitlementSnapshot{
		Plan: "s", State: domain.EntitlementStateActive, ModelTier: "s",
		Limits: map[domain.LimitKey]domain.LimitValue{},
	}
	assert.False(t, effectiveSetUnchanged(es, snap))
}

func TestEffectiveSetUnchanged_KeyMissingInSnap(t *testing.T) {
	v := int64(1)
	es := &domain.EffectiveSet{
		PlanCode: "s", State: domain.EntitlementStateActive, ModelTier: "s",
		Limits: map[domain.LimitKey]domain.LimitValue{
			domain.LimitKeyTenderLimit:     {Value: &v},
			domain.LimitKeyAPIRateLimitRPS: {Value: &v},
		},
	}
	snap := &domain.EntitlementSnapshot{
		Plan: "s", State: domain.EntitlementStateActive, ModelTier: "s",
		Limits: map[domain.LimitKey]domain.LimitValue{
			domain.LimitKeyTenderLimit:           {Value: &v},
			domain.LimitKeyWorkflowTemplateLimit: {Value: &v},
		},
	}
	assert.False(t, effectiveSetUnchanged(es, snap))
}

func TestEffectiveSetUnchanged_UnlimitedMismatch(t *testing.T) {
	v := int64(100)
	es := &domain.EffectiveSet{
		PlanCode: "s", State: domain.EntitlementStateActive, ModelTier: "s",
		Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {}}, // unlimited
	}
	snap := &domain.EntitlementSnapshot{
		Plan: "s", State: domain.EntitlementStateActive, ModelTier: "s",
		Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v}}, // bounded
	}
	assert.False(t, effectiveSetUnchanged(es, snap))
}

// ── computeChangedKeys ────────────────────────────────────────────────────

func TestComputeChangedKeys_NilPrev(t *testing.T) {
	v := int64(1)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyTenderLimit:     {Value: &v},
		domain.LimitKeyAPIRateLimitRPS: {},
	}
	keys := computeChangedKeys(limits, nil)
	assert.Len(t, keys, 2)
}

func TestComputeChangedKeys_SameValues_Empty(t *testing.T) {
	v := int64(100)
	limits := map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v}}
	prev := &domain.EntitlementSnapshot{
		Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v}},
	}
	assert.Empty(t, computeChangedKeys(limits, prev))
}

func TestComputeChangedKeys_ValueChanged(t *testing.T) {
	v1, v2 := int64(100), int64(200)
	limits := map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v2}}
	prev := &domain.EntitlementSnapshot{
		Limits: map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v1}},
	}
	keys := computeChangedKeys(limits, prev)
	assert.Contains(t, keys, string(domain.LimitKeyTenderLimit))
}

func TestComputeChangedKeys_KeyRemovedFromNew(t *testing.T) {
	v := int64(1)
	// prev has tenderLimit; new does NOT
	limits := map[domain.LimitKey]domain.LimitValue{domain.LimitKeyAPIRateLimitRPS: {Value: &v}}
	prev := &domain.EntitlementSnapshot{
		Limits: map[domain.LimitKey]domain.LimitValue{
			domain.LimitKeyAPIRateLimitRPS: {Value: &v},
			domain.LimitKeyTenderLimit:     {Value: &v},
		},
	}
	keys := computeChangedKeys(limits, prev)
	assert.Contains(t, keys, string(domain.LimitKeyTenderLimit))
}

// ── snapToEffectiveSet / effectiveSetToSnap ───────────────────────────────

func TestSnapToEffectiveSet_Roundtrip(t *testing.T) {
	tenantID := uuid.New()
	profile := "reduced_plan"
	v := int64(500)
	snap := &domain.EntitlementSnapshot{
		TenantID:     tenantID,
		Version:      3,
		Plan:         "starter",
		State:        domain.EntitlementStateActive,
		FeatureFlags: []string{"sso"},
		ModelTier:    "standard",
		Limits:       map[domain.LimitKey]domain.LimitValue{domain.LimitKeyTenderLimit: {Value: &v}},
		TrialProfile: &profile,
		Cause:        domain.CausePlanChange,
	}
	es := snapToEffectiveSet(snap)
	assert.Equal(t, tenantID, es.TenantID)
	assert.Equal(t, int64(3), es.Version)
	assert.Equal(t, "starter", es.PlanCode)
	assert.Equal(t, domain.EntitlementStateActive, es.State)
	assert.Equal(t, []string{"sso"}, es.FeatureFlags)
	assert.Equal(t, "standard", es.ModelTier)
	assert.Equal(t, &profile, es.TrialProfile)
	assert.Equal(t, domain.CausePlanChange, es.Cause)
	assert.NotNil(t, es.AddonEligibility)
}

func TestEffectiveSetToSnap_Roundtrip(t *testing.T) {
	tenantID := uuid.New()
	profile := "full_plan"
	es := &domain.EffectiveSet{
		TenantID:     tenantID,
		Version:      7,
		PlanCode:     "pro",
		State:        domain.EntitlementStateTrial,
		FeatureFlags: []string{"audit_log"},
		ModelTier:    "advanced",
		Limits:       map[domain.LimitKey]domain.LimitValue{},
		TrialProfile: &profile,
		Cause:        domain.CauseTrialChange,
		ComputedAt:   time.Now().UTC(),
	}
	snap := effectiveSetToSnap(es)
	assert.Equal(t, tenantID, snap.TenantID)
	assert.Equal(t, int64(7), snap.Version)
	assert.Equal(t, "pro", snap.Plan)
	assert.Equal(t, domain.EntitlementStateTrial, snap.State)
	assert.Equal(t, &profile, snap.TrialProfile)
}

// ── computeAddonEligibility ───────────────────────────────────────────────

func TestComputeAddonEligibility_Active_Bounded(t *testing.T) {
	v := int64(1000)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly:    {Value: &v},
		domain.LimitKeyAPIRequestBudgetMonthly: {Value: &v},
	}
	elig := computeAddonEligibility(domain.EntitlementStateActive, limits)
	assert.True(t, elig["llm_tokens"].Eligible)
	assert.True(t, elig["api_requests"].Eligible)
}

func TestComputeAddonEligibility_Active_Unlimited(t *testing.T) {
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly:    {}, // unlimited
		domain.LimitKeyAPIRequestBudgetMonthly: {}, // unlimited
	}
	elig := computeAddonEligibility(domain.EntitlementStateActive, limits)
	assert.False(t, elig["llm_tokens"].Eligible)
	assert.Equal(t, "unlimited_quota", *elig["llm_tokens"].Reason)
}

func TestComputeAddonEligibility_Active_MissingKey(t *testing.T) {
	limits := map[domain.LimitKey]domain.LimitValue{} // key not present → unlimited
	elig := computeAddonEligibility(domain.EntitlementStateActive, limits)
	assert.False(t, elig["llm_tokens"].Eligible)
	assert.Equal(t, "unlimited_quota", *elig["llm_tokens"].Reason)
}

func TestComputeAddonEligibility_Trial(t *testing.T) {
	v := int64(1000)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly:    {Value: &v},
		domain.LimitKeyAPIRequestBudgetMonthly: {Value: &v},
	}
	elig := computeAddonEligibility(domain.EntitlementStateTrial, limits)
	// HLD v0.69 P-5 (E-10): token packs allowed during a trial ...
	assert.True(t, elig["llm_tokens"].Eligible)
	assert.Nil(t, elig["llm_tokens"].Reason)
	// ... no other add-on category.
	assert.False(t, elig["api_requests"].Eligible)
	assert.Equal(t, "trial", *elig["api_requests"].Reason)
}

func TestComputeAddonEligibility_Trial_UnlimitedTokens(t *testing.T) {
	// P-5d: a trial with an unlimited token entitlement (e.g. full_plan on Enterprise) stays ineligible.
	limits := map[domain.LimitKey]domain.LimitValue{domain.LimitKeyLLMTokenQuotaMonthly: {Value: nil}}
	elig := computeAddonEligibility(domain.EntitlementStateTrial, limits)
	assert.False(t, elig["llm_tokens"].Eligible)
	assert.Equal(t, "unlimited_quota", *elig["llm_tokens"].Reason)
}

func TestComputeAddonEligibility_Suspended(t *testing.T) {
	limits := map[domain.LimitKey]domain.LimitValue{}
	elig := computeAddonEligibility(domain.EntitlementStateSuspended, limits)
	assert.False(t, elig["llm_tokens"].Eligible)
	assert.Equal(t, "not_active", *elig["llm_tokens"].Reason)
}

// ── applyTrialProfileLimits ───────────────────────────────────────────────

func TestApplyTrialProfileLimits_AllFields(t *testing.T) {
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly:    {Value: int64ptr(1_000_000)},
		domain.LimitKeyAPIRequestBudgetMonthly: {Value: int64ptr(50_000)},
		domain.LimitKeyAPIRateLimitRPS:         {Value: int64ptr(100)},
		domain.LimitKeyWorkflowTemplateLimit:   {Value: int64ptr(50)},
		domain.LimitKeyTenderLimit:             {Value: int64ptr(100)},
	}
	rps := 25
	wf := 5
	tender := 10
	profile := domain.TrialProfileLimits{
		LLMTokenQuotaMonthly:    int64ptr(200_000),
		APIRequestBudgetMonthly: int64ptr(2_000),
		APIRateLimitRPS:         &rps,
		WorkflowTemplateLimit:   &wf,
		TenderLimit:             &tender,
	}
	applyTrialProfileLimits(limits, profile)
	assert.Equal(t, int64(200_000), *limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
	assert.Equal(t, int64(2_000), *limits[domain.LimitKeyAPIRequestBudgetMonthly].Value)
	assert.Equal(t, int64(25), *limits[domain.LimitKeyAPIRateLimitRPS].Value)
	assert.Equal(t, int64(5), *limits[domain.LimitKeyWorkflowTemplateLimit].Value)
	assert.Equal(t, int64(10), *limits[domain.LimitKeyTenderLimit].Value)
}

func TestApplyTrialProfileLimits_NilFields_Unchanged(t *testing.T) {
	v := int64(999)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly: {Value: &v},
	}
	applyTrialProfileLimits(limits, domain.TrialProfileLimits{}) // empty profile → no-op
	assert.Equal(t, int64(999), *limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
}

// ── sortAddonsByExpiry ────────────────────────────────────────────────────

func TestSortAddonsByExpiry_Empty(t *testing.T) {
	var addons []domain.QuotaAddon
	sortAddonsByExpiry(addons) // must not panic
}

func TestSortAddonsByExpiry_EarlierFirst(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	later := time.Now().Add(48 * time.Hour)
	addons := []domain.QuotaAddon{
		{ID: uuid.New(), ExpiresAt: &later},
		{ID: uuid.New(), ExpiresAt: &soon},
	}
	sortAddonsByExpiry(addons)
	assert.True(t, addons[0].ExpiresAt.Equal(soon), "earlier expiry should be first")
}

func TestSortAddonsByExpiry_NilExpirySortsLast(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	addons := []domain.QuotaAddon{
		{ID: uuid.New(), ExpiresAt: nil}, // never expires → last
		{ID: uuid.New(), ExpiresAt: &soon},
	}
	sortAddonsByExpiry(addons)
	assert.NotNil(t, addons[0].ExpiresAt, "non-nil expiry should come first")
	assert.Nil(t, addons[1].ExpiresAt, "nil expiry should come last")
}

func TestSortAddonsByExpiry_BothNilStableRelativeOrder(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	addons := []domain.QuotaAddon{
		{ID: a, ExpiresAt: nil},
		{ID: b, ExpiresAt: nil},
	}
	sortAddonsByExpiry(addons)
	// Both nil: comparison returns false (neither before the other), stable in arbitrary order
	assert.Contains(t, []uuid.UUID{a, b}, addons[0].ID)
}

// helper used only in this file
func int64ptr(v int64) *int64 { return &v }

// ── computeAddonEligibility — non-Active states ────────────────────────────
// test/unit covers Active (bounded/unlimited), Trial, and Suspended.
// These tests pin the remaining states to the same "not_active" reason.

func TestComputeAddonEligibility_GracePeriod_NotActive(t *testing.T) {
	v := int64(1000)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly: {Value: &v},
	}
	elig := computeAddonEligibility(domain.EntitlementStateGracePeriod, limits)
	assert.False(t, elig["llm_tokens"].Eligible)
	require.NotNil(t, elig["llm_tokens"].Reason)
	assert.Equal(t, "not_active", *elig["llm_tokens"].Reason)
}

func TestComputeAddonEligibility_Offboarded_NotActive(t *testing.T) {
	v := int64(1000)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyAPIRequestBudgetMonthly: {Value: &v},
	}
	elig := computeAddonEligibility(domain.EntitlementStateOffboarded, limits)
	assert.False(t, elig["api_requests"].Eligible)
	require.NotNil(t, elig["api_requests"].Reason)
	assert.Equal(t, "not_active", *elig["api_requests"].Reason)
}

func TestComputeAddonEligibility_Provisioning_NotActive(t *testing.T) {
	limits := map[domain.LimitKey]domain.LimitValue{}
	elig := computeAddonEligibility(domain.EntitlementStateProvisioning, limits)
	assert.False(t, elig["llm_tokens"].Eligible)
	require.NotNil(t, elig["llm_tokens"].Reason)
	assert.Equal(t, "not_active", *elig["llm_tokens"].Reason)
}

// ── writeQuotaCeiling — targeted path coverage ─────────────────────────────
// test/unit tests this indirectly via Recompute. These white-box tests pin
// the two branches directly: LLM key present vs absent.

func TestWriteQuotaCeiling_LLMKeyPresent_CallsSetQuotaCeiling(t *testing.T) {
	cache := &trackingCache{}
	svc := NewEffectiveSetService(nil, nil, nil, nil, nil, nil, cache, nil)

	v := int64(100_000)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly: {Value: &v},
	}
	tenantID := uuid.New()
	svc.writeQuotaCeiling(context.Background(), tenantID, limits)

	require.Len(t, cache.ceilingCalls, 1)
	assert.Equal(t, tenantID, cache.ceilingCalls[0].tenantID)
	assert.Equal(t, domain.LimitKeyLLMTokenQuotaMonthly, cache.ceilingCalls[0].key)
	assert.Equal(t, v, *cache.ceilingCalls[0].ceiling)
}

func TestWriteQuotaCeiling_NoLLMKey_NoCacheCall(t *testing.T) {
	cache := &trackingCache{}
	svc := NewEffectiveSetService(nil, nil, nil, nil, nil, nil, cache, nil)

	v := int64(5000)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyAPIRateLimitRPS: {Value: &v}, // non-LLM key
	}
	svc.writeQuotaCeiling(context.Background(), uuid.New(), limits)
	assert.Empty(t, cache.ceilingCalls,
		"writeQuotaCeiling must not call SetQuotaCeiling for non-LLM keys")
}

func TestWriteQuotaCeiling_LLMKeyUnlimited_CallsWithNilCeiling(t *testing.T) {
	cache := &trackingCache{}
	svc := NewEffectiveSetService(nil, nil, nil, nil, nil, nil, cache, nil)

	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly: {}, // nil Value = unlimited
	}
	svc.writeQuotaCeiling(context.Background(), uuid.New(), limits)
	require.Len(t, cache.ceilingCalls, 1)
	assert.Nil(t, cache.ceilingCalls[0].ceiling,
		"unlimited LLM quota must pass nil ceiling to Valkey")
}

func TestWriteQuotaCeiling_CacheError_IsNonFatal(t *testing.T) {
	cache := &trackingCache{err: errors.New("valkey down")}
	svc := NewEffectiveSetService(nil, nil, nil, nil, nil, nil, cache, nil)

	v := int64(1000)
	limits := map[domain.LimitKey]domain.LimitValue{
		domain.LimitKeyLLMTokenQuotaMonthly: {Value: &v},
	}
	// Must not panic or return error — cache errors are non-fatal (CACHE-1).
	svc.writeQuotaCeiling(context.Background(), uuid.New(), limits)
}

// ── trackingCache ─────────────────────────────────────────────────────────

type ceilingCall struct {
	tenantID uuid.UUID
	key      domain.LimitKey
	ceiling  *int64
}

type trackingCache struct {
	mu           sync.Mutex
	ceilingCalls []ceilingCall
	err          error
}

func (c *trackingCache) GetEffectiveSet(_ context.Context, _ uuid.UUID) (*domain.EffectiveSet, error) {
	return nil, nil
}
func (c *trackingCache) SetEffectiveSet(_ context.Context, _ uuid.UUID, _ *domain.EffectiveSet) error {
	return nil
}
func (c *trackingCache) InvalidateEffectiveSet(_ context.Context, _ uuid.UUID) error { return nil }
func (c *trackingCache) SetQuotaCeiling(_ context.Context, tenantID uuid.UUID, key domain.LimitKey, ceiling *int64) error {
	c.mu.Lock()
	c.ceilingCalls = append(c.ceilingCalls, ceilingCall{tenantID, key, ceiling})
	c.mu.Unlock()
	return c.err
}
func (c *trackingCache) ResetUsageCounter(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ string) error {
	return nil
}
func (c *trackingCache) DeleteUsageCounters(_ context.Context, _ domain.LimitKey, _ string) (int64, error) {
	return 0, nil
}
func (c *trackingCache) GetPlan(_ context.Context, _ string) (*domain.Plan, error) { return nil, nil }
func (c *trackingCache) SetPlan(_ context.Context, _ *domain.Plan) error           { return nil }
func (c *trackingCache) InvalidatePlan(_ context.Context, _ string) error          { return nil }
func (c *trackingCache) Health(_ context.Context) error                            { return nil }
func (c *trackingCache) Close() error                                              { return nil }

var _ port.Cache = (*trackingCache)(nil)
