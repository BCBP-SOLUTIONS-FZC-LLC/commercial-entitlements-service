package domain

// Plan is the catalog entry for a subscription tier, fetched from IAM catalog
// admin service (CatalogService). JSON tags match iam-catalog-admin's
// PlanResponse wire shape exactly (snake_case — required for correct decoding
// since Go's JSON decoder does not strip underscores during case-insensitive
// matching, so fields like "llm_token_quota_monthly" would silently decode to
// nil without explicit tags).
type Plan struct {
	Code                    string                        `json:"code"`
	DisplayName             string                        `json:"display_name"`
	WorkflowTemplateLimit   *int                          `json:"workflow_template_limit"`
	TenderLimit             *int                          `json:"tender_limit"`
	TrialDurationDays       int                           `json:"trial_duration_days"`
	SSOEnabled              bool                          `json:"sso_enabled"`
	CustomBranding          string                        `json:"custom_branding"`
	AuditQueryWindowDays    int                           `json:"audit_query_window_days"`
	LLMTokenQuotaMonthly    *int64                        `json:"llm_token_quota_monthly"`    // nil = unlimited
	APIRequestBudgetMonthly *int64                        `json:"api_request_budget_monthly"` // nil = unlimited
	APIRateLimitRPS         int                           `json:"api_rate_limit_rps"`
	FeatureSet              map[string]interface{}        `json:"feature_set"`
	ModelTier               string                        `json:"model_tier"`
	RecordVersion           int64                         `json:"record_version"`
	TrialProfiles           map[string]TrialProfileLimits `json:"trial_profiles,omitempty"`
}

// TrialProfileLimits overrides specific limits when a tenant is in trial mode.
// Keys matching KnownLimitKeys take precedence over plan defaults during trial.
type TrialProfileLimits struct {
	LLMTokenQuotaMonthly    *int64 `json:"llm_token_quota_monthly,omitempty"`
	APIRequestBudgetMonthly *int64 `json:"api_request_budget_monthly,omitempty"`
	APIRateLimitRPS         *int   `json:"api_rate_limit_rps,omitempty"`
	WorkflowTemplateLimit   *int   `json:"workflow_template_limit,omitempty"`
	TenderLimit             *int   `json:"tender_limit,omitempty"`
}

// FeatureFlags extracts the string feature-flag keys from a Plan's FeatureSet.
func (p *Plan) FeatureFlags() []string {
	flags := make([]string, 0, len(p.FeatureSet))
	for k, v := range p.FeatureSet {
		if enabled, ok := v.(bool); ok && enabled {
			flags = append(flags, k)
		}
	}
	return flags
}

// BaseLimits returns the plan's limits as a map for merging.
func (p *Plan) BaseLimits() map[LimitKey]LimitValue {
	m := make(map[LimitKey]LimitValue, 5)
	m[LimitKeyLLMTokenQuotaMonthly] = LimitValue{Value: p.LLMTokenQuotaMonthly}
	m[LimitKeyAPIRequestBudgetMonthly] = LimitValue{Value: p.APIRequestBudgetMonthly}
	if p.APIRateLimitRPS > 0 {
		v := int64(p.APIRateLimitRPS)
		m[LimitKeyAPIRateLimitRPS] = LimitValue{Value: &v}
	} else {
		m[LimitKeyAPIRateLimitRPS] = LimitValue{Value: nil}
	}
	if p.WorkflowTemplateLimit != nil {
		v := int64(*p.WorkflowTemplateLimit)
		m[LimitKeyWorkflowTemplateLimit] = LimitValue{Value: &v}
	} else {
		m[LimitKeyWorkflowTemplateLimit] = LimitValue{Value: nil}
	}
	if p.TenderLimit != nil {
		v := int64(*p.TenderLimit)
		m[LimitKeyTenderLimit] = LimitValue{Value: &v}
	} else {
		m[LimitKeyTenderLimit] = LimitValue{Value: nil}
	}
	return m
}
