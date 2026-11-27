package unit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── LimitValue ─────────────────────────────────────────────────────────────

func TestLimitValue_IsUnlimited(t *testing.T) {
	assert.True(t, domain.LimitValue{Value: nil}.IsUnlimited())
	assert.False(t, domain.LimitValue{Value: i64ptr(0)}.IsUnlimited())
	assert.False(t, domain.LimitValue{Value: i64ptr(1_000_000)}.IsUnlimited())
}

// ── BillingResourceToLimitKey ──────────────────────────────────────────────

func TestBillingResourceToLimitKey(t *testing.T) {
	k, ok := domain.BillingResourceToLimitKey("llm_tokens")
	assert.True(t, ok)
	assert.Equal(t, domain.LimitKeyLLMTokenQuotaMonthly, k)

	k, ok = domain.BillingResourceToLimitKey("api_requests")
	assert.True(t, ok)
	assert.Equal(t, domain.LimitKeyAPIRequestBudgetMonthly, k)

	_, ok = domain.BillingResourceToLimitKey("unknown_resource")
	assert.False(t, ok)

	_, ok = domain.BillingResourceToLimitKey("")
	assert.False(t, ok)
}

// ── KnownLimitKeys ─────────────────────────────────────────────────────────

func TestKnownLimitKeys_ContainsAllCanonical(t *testing.T) {
	for _, key := range []domain.LimitKey{
		domain.LimitKeyLLMTokenQuotaMonthly,
		domain.LimitKeyAPIRequestBudgetMonthly,
		domain.LimitKeyAPIRateLimitRPS,
		domain.LimitKeyWorkflowTemplateLimit,
		domain.LimitKeyTenderLimit,
	} {
		assert.True(t, domain.KnownLimitKeys[key], "expected %s in KnownLimitKeys", key)
	}
	assert.False(t, domain.KnownLimitKeys["unknown_key"])
}

// ── Plan.FeatureFlags ──────────────────────────────────────────────────────

func TestPlan_FeatureFlags_ReturnsOnlyEnabled(t *testing.T) {
	p := &domain.Plan{
		FeatureSet: map[string]interface{}{
			"sso":          true,
			"audit_log":    true,
			"custom_brand": false,
			"not_a_bool":   "string_value",
			"zero":         0,
		},
	}
	flags := p.FeatureFlags()
	assert.ElementsMatch(t, []string{"sso", "audit_log"}, flags)
}

func TestPlan_FeatureFlags_EmptyFeatureSet(t *testing.T) {
	p := &domain.Plan{FeatureSet: map[string]interface{}{}}
	assert.Empty(t, p.FeatureFlags())
}

func TestPlan_FeatureFlags_NilFeatureSet(t *testing.T) {
	p := &domain.Plan{}
	assert.Empty(t, p.FeatureFlags())
}

// ── Plan.BaseLimits ────────────────────────────────────────────────────────

func TestPlan_BaseLimits_AllFieldsSet(t *testing.T) {
	wf := 50
	tender := 100
	p := &domain.Plan{
		LLMTokenQuotaMonthly:    i64ptr(1_000_000),
		APIRequestBudgetMonthly: i64ptr(50_000),
		APIRateLimitRPS:         200,
		WorkflowTemplateLimit:   &wf,
		TenderLimit:             &tender,
	}
	limits := p.BaseLimits()

	assert.Equal(t, i64ptr(1_000_000), limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
	assert.Equal(t, i64ptr(50_000), limits[domain.LimitKeyAPIRequestBudgetMonthly].Value)
	assert.Equal(t, i64ptr(200), limits[domain.LimitKeyAPIRateLimitRPS].Value)
	assert.Equal(t, i64ptr(50), limits[domain.LimitKeyWorkflowTemplateLimit].Value)
	assert.Equal(t, i64ptr(100), limits[domain.LimitKeyTenderLimit].Value)
}

func TestPlan_BaseLimits_NilOptionalFields(t *testing.T) {
	p := &domain.Plan{
		APIRateLimitRPS: 0,
	}
	limits := p.BaseLimits()

	assert.True(t, limits[domain.LimitKeyLLMTokenQuotaMonthly].IsUnlimited())
	assert.True(t, limits[domain.LimitKeyAPIRequestBudgetMonthly].IsUnlimited())
	assert.True(t, limits[domain.LimitKeyAPIRateLimitRPS].IsUnlimited())
	assert.True(t, limits[domain.LimitKeyWorkflowTemplateLimit].IsUnlimited())
	assert.True(t, limits[domain.LimitKeyTenderLimit].IsUnlimited())
}

func TestPlan_BaseLimits_UnlimitedLLMButBoundedAPI(t *testing.T) {
	p := &domain.Plan{
		LLMTokenQuotaMonthly:    nil,
		APIRequestBudgetMonthly: i64ptr(999),
	}
	limits := p.BaseLimits()
	assert.True(t, limits[domain.LimitKeyLLMTokenQuotaMonthly].IsUnlimited())
	assert.Equal(t, i64ptr(999), limits[domain.LimitKeyAPIRequestBudgetMonthly].Value)
}

// ── DomainError ────────────────────────────────────────────────────────────

func TestDomainError_Error(t *testing.T) {
	e := domain.NewError(domain.ErrOverrideNotFound, "override not found for key")
	assert.Equal(t, "override_not_found: override not found for key", e.Error())
}

func TestDomainError_Unwrap(t *testing.T) {
	e := domain.NewError(domain.ErrAddonNotFound, "no addon")
	assert.True(t, errors.Is(e, domain.ErrAddonNotFound))
}

func TestDomainError_WithDetails(t *testing.T) {
	e := domain.NewError(domain.ErrInvalidValue, "bad value")
	e2 := e.WithDetails(map[string]any{"key": "llm_tokens", "max": 1000})
	assert.Equal(t, e, e2, "WithDetails returns the same pointer")
	assert.Equal(t, "llm_tokens", e.Details["key"])
}

func TestDomainError_NewError_AllSentinels(t *testing.T) {
	sentinels := []error{
		domain.ErrTenantNotFound,
		domain.ErrTrialAssignmentNotFound,
		domain.ErrTrialAlreadyActive,
		domain.ErrTrialEndsAtInPast,
		domain.ErrTrialOverrideNotFound,
		domain.ErrUpstreamUnavailable,
		domain.ErrOverrideNotFound,
		domain.ErrOptimisticLockConflict,
		domain.ErrConcurrentUpdate,
		domain.ErrConflict,
		domain.ErrInvalidLimitKey,
		domain.ErrInvalidValue,
		domain.ErrRPSCannotBeUnlimited,
		domain.ErrExpiresAtInPast,
		domain.ErrReasonRequired,
		domain.ErrInvalidProfile,
		domain.ErrInvalidAction,
		domain.ErrTokensRequired,
		domain.ErrPackPartiallyUsed,
		domain.ErrInvalidPeriodStart,
		domain.ErrDBUnavailable,
		domain.ErrForbidden,
		domain.ErrOperatorRoleRequired,
		domain.ErrDrawdownAlreadyRecorded,
		domain.ErrShortfallNotFound,
		domain.ErrShortfallAlreadyResolved,
		domain.ErrDispositionRefRequired,
		domain.ErrAddonNotFound,
		domain.ErrPlanNotFound,
		domain.ErrSnapshotNotFound,
		domain.ErrDependencyUnavailable,
		domain.ErrRequestTimeout,
		domain.ErrCatalogUnavailable,
		domain.ErrBillingRefAlreadyClaimed,
	}
	for _, s := range sentinels {
		e := domain.NewError(s, "msg")
		assert.Equal(t, s.Error(), e.Code)
		assert.True(t, errors.Is(e, s))
	}
}

// ── IsProducedEvent ────────────────────────────────────────────────────────

func TestIsProducedEvent_ProducedType(t *testing.T) {
	assert.True(t, domain.IsProducedEvent(domain.EventTypeTenantEntitlementsChanged))
}

func TestIsProducedEvent_UnknownType(t *testing.T) {
	assert.False(t, domain.IsProducedEvent("TenantPlanChanged"))
	assert.False(t, domain.IsProducedEvent(""))
	assert.False(t, domain.IsProducedEvent("random_event"))
}

// ── TopicForEvent ──────────────────────────────────────────────────────────

func TestTopicForEvent_AlwaysReturnsEntitlements(t *testing.T) {
	for _, et := range []string{
		domain.EventTypeTenantEntitlementsChanged,
		"anything_else",
		"",
	} {
		assert.Equal(t, domain.TopicEntitlements, domain.TopicForEvent(et))
	}
}

// ── NewEvent / NewTenantEvent ──────────────────────────────────────────────

func TestNewEvent_SetsTypeAndData(t *testing.T) {
	payload := map[string]string{"foo": "bar"}
	ev := domain.NewEvent("test_event", payload)
	assert.Equal(t, "test_event", ev.Type)
	assert.Equal(t, payload, ev.Data)
	assert.False(t, ev.OccurredAt.IsZero())
}

func TestNewTenantEvent_SetsTenantAndSubject(t *testing.T) {
	tenantID := uuid.New()
	ev := domain.NewTenantEvent("test_event", tenantID, nil)
	assert.Equal(t, tenantID, ev.TenantID)
	assert.Equal(t, tenantID.String(), ev.Subject)
	assert.Equal(t, "test_event", ev.Type)
}

// ── EntitlementState constants ─────────────────────────────────────────────

func TestEntitlementState_Constants(t *testing.T) {
	assert.Equal(t, domain.EntitlementState("active"), domain.EntitlementStateActive)
	assert.Equal(t, domain.EntitlementState("trial"), domain.EntitlementStateTrial)
	assert.Equal(t, domain.EntitlementState("grace_period"), domain.EntitlementStateGracePeriod)
	assert.Equal(t, domain.EntitlementState("suspended"), domain.EntitlementStateSuspended)
	assert.Equal(t, domain.EntitlementState("offboarded"), domain.EntitlementStateOffboarded)
	assert.Equal(t, domain.EntitlementState("provisioning"), domain.EntitlementStateProvisioning)
}

// ── EffectiveSetCause constants ────────────────────────────────────────────

func TestEffectiveSetCause_Constants(t *testing.T) {
	causes := []domain.EffectiveSetCause{
		domain.CausePlanChange, domain.CauseOverrideChange, domain.CauseAddonChange,
		domain.CauseTrialChange, domain.CauseCatalogRefresh, domain.CauseBillingEvent,
		domain.CauseManualRecompute, domain.CauseTenantLifecycle, domain.CauseShortfallResolution,
	}
	for _, c := range causes {
		assert.NotEmpty(t, string(c))
	}
}

// ── TrialProfile / ValidTrialProfiles ─────────────────────────────────────

func TestValidTrialProfiles_ContainsAllProfiles(t *testing.T) {
	for _, p := range []domain.TrialProfile{
		domain.TrialProfileTrialSpecific,
		domain.TrialProfileReducedPlan,
		domain.TrialProfileFullPlan,
		domain.TrialProfileCustom,
	} {
		assert.True(t, domain.ValidTrialProfiles[p], "expected %s in ValidTrialProfiles", p)
	}
	assert.False(t, domain.ValidTrialProfiles["nonexistent"])
}

// ── QuotaAddonStatus constants ─────────────────────────────────────────────

func TestQuotaAddonStatus_Constants(t *testing.T) {
	assert.Equal(t, domain.QuotaAddonStatus("active"), domain.AddonStatusActive)
	assert.Equal(t, domain.QuotaAddonStatus("expired"), domain.AddonStatusExpired)
	assert.Equal(t, domain.QuotaAddonStatus("consumed"), domain.AddonStatusConsumed)
	assert.Equal(t, domain.QuotaAddonStatus("revoked"), domain.AddonStatusRevoked)
}

// ── OverrideAction constants ───────────────────────────────────────────────

func TestOverrideAction_Constants(t *testing.T) {
	assert.Equal(t, domain.OverrideAction("set"), domain.ActionSet)
	assert.Equal(t, domain.OverrideAction("removed"), domain.ActionRemoved)
	assert.Equal(t, domain.OverrideAction("expired"), domain.ActionExpired)
}

// ── ShortfallStatus / ShortfallDisposition constants ──────────────────────

func TestShortfallStatus_Constants(t *testing.T) {
	for _, s := range []domain.ShortfallStatus{
		domain.ShortfallStatusPending,
		domain.ShortfallStatusAutoAccepted,
		domain.ShortfallStatusPendingApproval,
		domain.ShortfallStatusApproved,
		domain.ShortfallStatusRejected,
		domain.ShortfallStatusExpired,
	} {
		assert.NotEmpty(t, string(s))
	}
}

func TestShortfallDisposition_Constants(t *testing.T) {
	assert.Equal(t, domain.ShortfallDisposition("approve"), domain.DispositionApprove)
	assert.Equal(t, domain.ShortfallDisposition("reject"), domain.DispositionReject)
}

// ── Struct constructors / zero values ─────────────────────────────────────

func TestEntitlementSnapshot_ZeroValue(t *testing.T) {
	var snap domain.EntitlementSnapshot
	assert.Equal(t, uuid.Nil, snap.TenantID)
	assert.Zero(t, snap.Version)
}

func TestQuotaAddon_ZeroValue(t *testing.T) {
	var a domain.QuotaAddon
	assert.Equal(t, uuid.Nil, a.ID)
	assert.Equal(t, domain.QuotaAddonStatus(""), a.Status)
}

func TestQuotaAddonShortfall_ZeroValue(t *testing.T) {
	var sf domain.QuotaAddonShortfall
	assert.Equal(t, uuid.Nil, sf.ID)
	assert.Nil(t, sf.Disposition)
}

func TestTenantTrialAssignment_Fields(t *testing.T) {
	now := time.Now().UTC()
	a := domain.TenantTrialAssignment{
		TenantID:      uuid.New(),
		PlanCode:      "enterprise",
		Profile:       domain.TrialProfileFullPlan,
		StartsAt:      now,
		EndsAt:        now.Add(30 * 24 * time.Hour),
		RecordVersion: 1,
	}
	assert.Equal(t, "enterprise", a.PlanCode)
	assert.Equal(t, domain.TrialProfileFullPlan, a.Profile)
	require.True(t, a.EndsAt.After(a.StartsAt))
}

func TestTrialProfileLimits_Fields(t *testing.T) {
	rps := 50
	tpl := domain.TrialProfileLimits{
		LLMTokenQuotaMonthly:    i64ptr(500_000),
		APIRequestBudgetMonthly: i64ptr(5_000),
		APIRateLimitRPS:         &rps,
		WorkflowTemplateLimit:   iptr(10),
		TenderLimit:             iptr(5),
	}
	assert.Equal(t, int64(500_000), *tpl.LLMTokenQuotaMonthly)
	assert.Equal(t, int64(5_000), *tpl.APIRequestBudgetMonthly)
	assert.Equal(t, 50, *tpl.APIRateLimitRPS)
}

func TestTenantEntitlementsChangedPayload_Fields(t *testing.T) {
	tenantID := uuid.New()
	p := domain.TenantEntitlementsChangedPayload{
		TenantID:        tenantID,
		Version:         3,
		Cause:           domain.CausePlanChange,
		ChangedKeys:     []string{"llm_token_quota_monthly"},
		PreviousVersion: 2,
		ComputedAt:      time.Now().UTC(),
	}
	assert.Equal(t, tenantID, p.TenantID)
	assert.Equal(t, int64(3), p.Version)
	assert.Equal(t, int64(2), p.PreviousVersion)
}

func TestInboundBillingAddonPurchaseEvent_Fields(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	ev := domain.InboundBillingAddonPurchaseEvent{
		TenantID:    tenantID,
		Resource:    "llm_tokens",
		Delta:       100_000,
		Reason:      "purchase",
		BillingRef:  "inv_123",
		PurchasedAt: now,
	}
	assert.Equal(t, "purchase", ev.Reason)
	assert.Equal(t, int64(100_000), ev.Delta)
}

func TestInboundBillingAddonRefundEvent_Fields(t *testing.T) {
	ev := domain.InboundBillingAddonRefundEvent{
		TenantID:    uuid.New(),
		Resource:    "llm_tokens",
		Delta:       -50_000,
		Reason:      "refund",
		BillingRef:  uuid.New(),
		PurchaseRef: uuid.New(),
	}
	assert.Equal(t, int64(-50_000), ev.Delta)
	assert.Equal(t, "refund", ev.Reason)
}

func TestAddonResourceEligibility_Fields(t *testing.T) {
	reason := "trial"
	e := domain.AddonResourceEligibility{Eligible: false, Reason: &reason}
	assert.False(t, e.Eligible)
	assert.Equal(t, "trial", *e.Reason)
}
