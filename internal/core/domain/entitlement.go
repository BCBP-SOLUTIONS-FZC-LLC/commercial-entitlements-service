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
	// AddonEligible is true when the tenant's current state and plan allow
	// purchasing quota add-on packs. Billing reads this field for the
	// BIL-ADD-1 finite-quota eligibility check (BL-OQ-3).
	AddonEligible bool `json:"addon_eligible"`
}
