package domain

import (
	"time"

	"github.com/google/uuid"
)

// LimitKey identifies a quota or capability dimension in the EffectiveSet.
type LimitKey string

const (
	LimitKeyLLMTokenQuotaMonthly    LimitKey = "llm_token_quota_monthly"
	LimitKeyAPIRequestBudgetMonthly LimitKey = "api_request_budget_monthly"
	LimitKeyAPIRateLimitRPS         LimitKey = "api_rate_limit_rps"
	LimitKeyWorkflowTemplateLimit   LimitKey = "workflow_template_limit"
	LimitKeyTenderLimit             LimitKey = "tender_limit"
)

// KnownLimitKeys is the authoritative set of keys the service manages.
var KnownLimitKeys = map[LimitKey]bool{
	LimitKeyLLMTokenQuotaMonthly:    true,
	LimitKeyAPIRequestBudgetMonthly: true,
	LimitKeyAPIRateLimitRPS:         true,
	LimitKeyWorkflowTemplateLimit:   true,
	LimitKeyTenderLimit:             true,
}

// billingResourceKeys maps Billing's short resource names (from TenantQuotaAddOnChanged)
// to domain LimitKeys. Only quota-bearing keys are add-on eligible (BL-OQ-3).
var billingResourceKeys = map[string]LimitKey{
	"llm_tokens":   LimitKeyLLMTokenQuotaMonthly,
	"api_requests": LimitKeyAPIRequestBudgetMonthly,
}

// BillingResourceToLimitKey maps a Billing resource name to a domain LimitKey.
// Returns false if the resource name is unknown or not add-on eligible.
func BillingResourceToLimitKey(resource string) (LimitKey, bool) {
	k, ok := billingResourceKeys[resource]
	return k, ok
}

// LimitValue holds a numeric limit; nil Value means unlimited.
type LimitValue struct {
	// Value is nil when the dimension is unlimited.
	Value *int64 `json:"value"`
}

// IsUnlimited reports whether this limit is unbounded.
func (lv LimitValue) IsUnlimited() bool { return lv.Value == nil }

// EntitlementState models the lifecycle state of a tenant's subscription.
type EntitlementState string

const (
	EntitlementStateActive       EntitlementState = "active"
	EntitlementStateTrial        EntitlementState = "trial"
	EntitlementStateGracePeriod  EntitlementState = "grace_period"
	EntitlementStateSuspended    EntitlementState = "suspended"
	EntitlementStateOffboarded   EntitlementState = "offboarded"
	EntitlementStateProvisioning EntitlementState = "provisioning"
)

// EffectiveSetCause describes why a Recompute was triggered.
type EffectiveSetCause string

const (
	CausePlanChange          EffectiveSetCause = "plan_change"
	CauseOverrideChange      EffectiveSetCause = "override_change"
	CauseAddonChange         EffectiveSetCause = "addon_change"
	CauseTrialChange         EffectiveSetCause = "trial_change"
	CauseCatalogRefresh      EffectiveSetCause = "catalog_refresh"
	CauseBillingEvent        EffectiveSetCause = "billing_event"
	CauseManualRecompute     EffectiveSetCause = "manual_recompute"
	CauseTenantLifecycle     EffectiveSetCause = "tenant_lifecycle"
	CauseShortfallResolution EffectiveSetCause = "shortfall_resolution"
)

// EffectiveSet is the computed, merged entitlement set for one tenant at a
// point in time. It is derived by the EffectiveSetService from plan baseline,
// trial-profile, trial overrides, operator overrides, and addon quota balance.
type EffectiveSet struct {
	TenantID     uuid.UUID               `json:"tenant_id"`
	Version      int64                   `json:"version"`
	PlanCode     string                  `json:"plan_code"`
	State        EntitlementState        `json:"state"`
	FeatureFlags []string                `json:"feature_flags"`
	ModelTier    string                  `json:"model_tier"`
	Limits       map[LimitKey]LimitValue `json:"limits"`
	TrialProfile *string                 `json:"trial_profile,omitempty"`
	Cause        EffectiveSetCause       `json:"cause"`
	ComputedAt   time.Time               `json:"computed_at"`
	// AddonEligibility reports per-resource eligibility for quota add-on purchases.
	// Billing reads llm_tokens in v1 for the BIL-ADD-1 check (BL-OQ-3).
	// Keys: "llm_tokens", "api_requests". Reasons: "trial", "not_active", "unlimited_quota".
	AddonEligibility map[string]AddonResourceEligibility `json:"addon_eligibility"`
}

// AddonResourceEligibility reports whether a specific resource type can receive
// quota add-on packs and, if not, why.
type AddonResourceEligibility struct {
	Eligible bool    `json:"eligible"`
	Reason   *string `json:"reason"`
}
