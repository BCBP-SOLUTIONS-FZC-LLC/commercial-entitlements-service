package http

import (
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── Effective Set ─────────────────────────────────────────────────────────

type EffectiveSetResponse struct {
	TenantID     uuid.UUID                     `json:"tenant_id"`
	Version      int64                         `json:"version"`
	PlanCode     string                        `json:"plan_code"`
	State        string                        `json:"state"`
	FeatureFlags []string                      `json:"feature_flags"`
	ModelTier    string                        `json:"model_tier"`
	Limits       map[string]LimitValueResponse `json:"limits"`
	TrialProfile *string                       `json:"trial_profile,omitempty"`
	Cause        string                        `json:"cause"`
	ComputedAt   time.Time                     `json:"computed_at"`
}

type LimitValueResponse struct {
	Value *int64 `json:"value"` // null means unlimited
}

func effectiveSetToResponse(es *domain.EffectiveSet) EffectiveSetResponse {
	limits := make(map[string]LimitValueResponse, len(es.Limits))
	for k, v := range es.Limits {
		limits[string(k)] = LimitValueResponse{Value: v.Value}
	}
	return EffectiveSetResponse{
		TenantID:     es.TenantID,
		Version:      es.Version,
		PlanCode:     es.PlanCode,
		State:        string(es.State),
		FeatureFlags: es.FeatureFlags,
		ModelTier:    es.ModelTier,
		Limits:       limits,
		TrialProfile: es.TrialProfile,
		Cause:        string(es.Cause),
		ComputedAt:   es.ComputedAt,
	}
}

// ── Override ──────────────────────────────────────────────────────────────

type SetOverrideBody struct {
	Value     *int64     `json:"value"`                           // null = unlimited
	Reason    string     `json:"reason" binding:"required"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type RemoveOverrideBody struct {
	Reason string `json:"reason" binding:"required"`
}

type OverrideResponse struct {
	TenantID      uuid.UUID  `json:"tenant_id"`
	LimitKey      string     `json:"limit_key"`
	Value         *int64     `json:"value"`
	Reason        string     `json:"reason"`
	SetBy         uuid.UUID  `json:"set_by"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	RecordVersion int64      `json:"record_version"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type OverrideHistoryResponse struct {
	ID        int64      `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	LimitKey  string     `json:"limit_key"`
	OldValue  *int64     `json:"old_value"`
	NewValue  *int64     `json:"new_value"`
	Action    string     `json:"action"`
	Reason    string     `json:"reason"`
	ActorID   uuid.UUID  `json:"actor_id"`
	CreatedAt time.Time  `json:"created_at"`
}

func overrideToResponse(o *domain.Override) OverrideResponse {
	return OverrideResponse{
		TenantID:      o.TenantID,
		LimitKey:      string(o.LimitKey),
		Value:         o.Value,
		Reason:        o.Reason,
		SetBy:         o.SetBy,
		ExpiresAt:     o.ExpiresAt,
		RecordVersion: o.RecordVersion,
		UpdatedAt:     o.UpdatedAt,
	}
}

func overrideHistoryToResponse(h domain.OverrideHistory) OverrideHistoryResponse {
	return OverrideHistoryResponse{
		ID:        h.ID,
		TenantID:  h.TenantID,
		LimitKey:  string(h.LimitKey),
		OldValue:  h.OldValue,
		NewValue:  h.NewValue,
		Action:    string(h.Action),
		Reason:    h.Reason,
		ActorID:   h.ActorID,
		CreatedAt: h.CreatedAt,
	}
}

// ── Addon ─────────────────────────────────────────────────────────────────

type AddAddonBody struct {
	LimitKey    string     `json:"limit_key" binding:"required"`
	TotalTokens int64      `json:"total_tokens" binding:"required,gt=0"`
	Reason      string     `json:"reason" binding:"required"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type AddonResponse struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        uuid.UUID  `json:"tenant_id"`
	LimitKey        string     `json:"limit_key"`
	TotalTokens     int64      `json:"total_tokens"`
	RemainingTokens int64      `json:"remaining_tokens"`
	GrantedBy       uuid.UUID  `json:"granted_by"`
	Reason          string     `json:"reason"`
	Status          string     `json:"status"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	RecordVersion   int64      `json:"record_version"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func addonToResponse(a *domain.QuotaAddon) AddonResponse {
	return AddonResponse{
		ID:              a.ID,
		TenantID:        a.TenantID,
		LimitKey:        string(a.LimitKey),
		TotalTokens:     a.TotalTokens,
		RemainingTokens: a.RemainingTokens,
		GrantedBy:       a.GrantedBy,
		Reason:          a.Reason,
		Status:          string(a.Status),
		ExpiresAt:       a.ExpiresAt,
		RecordVersion:   a.RecordVersion,
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
	}
}

// ── Drawdown ──────────────────────────────────────────────────────────────

type RecordDrawdownBody struct {
	LimitKey       string    `json:"limit_key" binding:"required"`
	TokensConsumed int64     `json:"tokens_consumed" binding:"required,gt=0"`
	PeriodStart    time.Time `json:"period_start" binding:"required"`
	RequestID      *string   `json:"request_id,omitempty"` // idempotency key
}

type DrawdownResponse struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"tenant_id"`
	LimitKey       string    `json:"limit_key"`
	TokensConsumed int64     `json:"tokens_consumed"`
	PeriodStart    time.Time `json:"period_start"`
	RequestID      *uuid.UUID `json:"request_id,omitempty"`
	RecordedAt     time.Time `json:"recorded_at"`
}

func drawdownToResponse(d *domain.QuotaAddonDrawdown) DrawdownResponse {
	return DrawdownResponse{
		ID:             d.ID,
		TenantID:       d.TenantID,
		LimitKey:       string(d.LimitKey),
		TokensConsumed: d.TokensConsumed,
		PeriodStart:    d.PeriodStart,
		RequestID:      d.RequestID,
		RecordedAt:     d.RecordedAt,
	}
}

// ── Shortfall ─────────────────────────────────────────────────────────────

type ShortfallResponse struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        uuid.UUID  `json:"tenant_id"`
	DrawdownID      uuid.UUID  `json:"drawdown_id"`
	LimitKey        string     `json:"limit_key"`
	Requested       int64      `json:"requested"`
	Available       int64      `json:"available"`
	Shortfall       int64      `json:"shortfall"`
	Status          string     `json:"status"`
	Disposition     *string    `json:"disposition,omitempty"`
	DispositionBy   *uuid.UUID `json:"disposition_by,omitempty"`
	DispositionRef  *string    `json:"disposition_ref,omitempty"`
	DispositionNote *string    `json:"disposition_note,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

type ResolveShortfallBody struct {
	Disposition    string  `json:"disposition" binding:"required"`
	DispositionRef *string `json:"disposition_ref,omitempty"`
	Note           *string `json:"note,omitempty"`
}

func shortfallToResponse(s *domain.QuotaAddonShortfall) ShortfallResponse {
	var disp *string
	if s.Disposition != nil {
		d := string(*s.Disposition)
		disp = &d
	}
	return ShortfallResponse{
		ID:              s.ID,
		TenantID:        s.TenantID,
		DrawdownID:      s.DrawdownID,
		LimitKey:        string(s.LimitKey),
		Requested:       s.Requested,
		Available:       s.Available,
		Shortfall:       s.Shortfall,
		Status:          string(s.Status),
		Disposition:     disp,
		DispositionBy:   s.DispositionBy,
		DispositionRef:  s.DispositionRef,
		DispositionNote: s.DispositionNote,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		ResolvedAt:      s.ResolvedAt,
	}
}

// ── Trial ─────────────────────────────────────────────────────────────────

type AssignTrialProfileBody struct {
	PlanCode string    `json:"plan_code" binding:"required"`
	Profile  string    `json:"profile" binding:"required"`
	StartsAt time.Time `json:"starts_at" binding:"required"`
	EndsAt   time.Time `json:"ends_at" binding:"required"`
}

type TrialAssignmentResponse struct {
	TenantID      uuid.UUID `json:"tenant_id"`
	PlanCode      string    `json:"plan_code"`
	Profile       string    `json:"profile"`
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
	SetBy         uuid.UUID `json:"set_by"`
	RecordVersion int64     `json:"record_version"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type SetTrialOverrideBody struct {
	Value *int64 `json:"value"` // null = unlimited
}

type TrialOverrideResponse struct {
	TenantID  uuid.UUID `json:"tenant_id"`
	LimitKey  string    `json:"limit_key"`
	Value     *int64    `json:"value"`
	SetBy     uuid.UUID `json:"set_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

func trialAssignmentToResponse(a *domain.TenantTrialAssignment) TrialAssignmentResponse {
	return TrialAssignmentResponse{
		TenantID:      a.TenantID,
		PlanCode:      a.PlanCode,
		Profile:       string(a.Profile),
		StartsAt:      a.StartsAt,
		EndsAt:        a.EndsAt,
		SetBy:         a.SetBy,
		RecordVersion: a.RecordVersion,
		UpdatedAt:     a.UpdatedAt,
	}
}

func trialOverrideToResponse(o *domain.TrialEntitlementOverride) TrialOverrideResponse {
	return TrialOverrideResponse{
		TenantID:  o.TenantID,
		LimitKey:  string(o.LimitKey),
		Value:     o.Value,
		SetBy:     o.SetBy,
		UpdatedAt: o.UpdatedAt,
	}
}

// ── Plan ──────────────────────────────────────────────────────────────────

type PlanResponse struct {
	Code                    string            `json:"code"`
	DisplayName             string            `json:"display_name"`
	ModelTier               string            `json:"model_tier"`
	LLMTokenQuotaMonthly    *int64            `json:"llm_token_quota_monthly"`
	APIRequestBudgetMonthly *int64            `json:"api_request_budget_monthly"`
	APIRateLimitRPS         int               `json:"api_rate_limit_rps"`
	WorkflowTemplateLimit   *int              `json:"workflow_template_limit"`
	TenderLimit             *int              `json:"tender_limit"`
	RecordVersion           int64             `json:"record_version"`
}

func planToResponse(p *domain.Plan) PlanResponse {
	return PlanResponse{
		Code:                    p.Code,
		DisplayName:             p.DisplayName,
		ModelTier:               p.ModelTier,
		LLMTokenQuotaMonthly:    p.LLMTokenQuotaMonthly,
		APIRequestBudgetMonthly: p.APIRequestBudgetMonthly,
		APIRateLimitRPS:         p.APIRateLimitRPS,
		WorkflowTemplateLimit:   p.WorkflowTemplateLimit,
		TenderLimit:             p.TenderLimit,
		RecordVersion:           p.RecordVersion,
	}
}

// ── Actor extraction helpers ──────────────────────────────────────────────
// (actor extraction is handled by actorFromClaims in override_handler.go)
