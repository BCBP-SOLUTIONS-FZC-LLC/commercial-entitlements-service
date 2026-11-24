package domain

// Plan is the catalog entry for a subscription tier, fetched from IAM catalog
// admin service (CatalogService).
type Plan struct {
	Code                    string
	DisplayName             string
	WorkflowTemplateLimit   *int
	TenderLimit             *int
	TrialDurationDays       int
	SSOEnabled              bool
	CustomBranding          string
	AuditQueryWindowDays    int
	LLMTokenQuotaMonthly    *int64 // nil = unlimited
	APIRequestBudgetMonthly *int64 // nil = unlimited
	APIRateLimitRPS         int
	FeatureSet              map[string]interface{}
	ModelTier               string
	RecordVersion           int64
	TrialProfiles           map[string]TrialProfileLimits
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
