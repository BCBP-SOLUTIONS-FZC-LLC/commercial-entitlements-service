package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── Service interfaces ─────────────────────────────────────────────────────

// EffectiveSetService is the read-side API: resolves a tenant's current entitlements.
type EffectiveSetService interface {
	GetEffectiveSet(ctx context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error)
	Recompute(ctx context.Context, tenantID uuid.UUID, cause domain.EffectiveSetCause) (*domain.EffectiveSet, error)
}

// OverrideService manages operator-level manual limit overrides.
type OverrideService interface {
	SetOverride(ctx context.Context, req SetOverrideRequest) (*domain.Override, error)
	RemoveOverride(ctx context.Context, req RemoveOverrideRequest) error
	GetOverride(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) (*domain.Override, error)
	ListOverrides(ctx context.Context, tenantID uuid.UUID) ([]domain.Override, error)
	ListOverrideHistory(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) ([]domain.OverrideHistory, error)
	ExpireOverrides(ctx context.Context) (int, error)
}

// AddonService manages quota addon packs and drawdowns with full shortfall lifecycle.
type AddonService interface {
	AddAddon(ctx context.Context, req AddAddonRequest) (*domain.QuotaAddon, error)
	RemoveAddon(ctx context.Context, req RemoveAddonRequest) error
	ListAddons(ctx context.Context, tenantID uuid.UUID) ([]domain.QuotaAddon, error)
	RecordDrawdown(ctx context.Context, req RecordDrawdownRequest) (*domain.QuotaAddonDrawdown, error)
	ApplyBillingRefund(ctx context.Context, req ApplyBillingRefundRequest) error
	// ApplyBillingPurchase creates a new quota addon pack from a confirmed
	// Billing purchase event (TenantQuotaAddOnChanged {reason: "purchase"}).
	// Idempotent on BillingRef; expiry is PurchasedAt + 12 months (E-1 / BL-OQ-4).
	ApplyBillingPurchase(ctx context.Context, req ApplyBillingPurchaseRequest) error
	ListDrawdowns(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddonDrawdown, error)
	ResolveShortfall(ctx context.Context, req ResolveShortfallRequest) (*domain.QuotaAddonShortfall, error)
	ListShortfalls(ctx context.Context, tenantID uuid.UUID, statusFilter []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error)
	GetShortfall(ctx context.Context, id uuid.UUID) (*domain.QuotaAddonShortfall, error)
	ExpireAddons(ctx context.Context) (int, error)
	AddonForfeiter
}

// AddonForfeiter forfeits a purged tenant's add-on balance (E-9, CD-55,
// HLD v0.69 P-5a). Consumers of TenantMembershipsPurged depend on this
// narrow interface only.
type AddonForfeiter interface {
	// ForfeitAddonsAtPurge moves every active pack of the tenant to status
	// forfeited with a zero balance, in the caller's transaction, and writes
	// one billing.token_addon.revoked AL-5 entry per pack. Idempotent: a
	// second call finds no active pack. Returns the number of packs forfeited.
	ForfeitAddonsAtPurge(ctx context.Context, tenantID uuid.UUID) (int, error)
}

// ShortfallService lists and resolves quota shortfalls.
type ShortfallService interface {
	ListShortfalls(ctx context.Context, tenantID uuid.UUID, statusFilter []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error)
	GetShortfall(ctx context.Context, id uuid.UUID) (*domain.QuotaAddonShortfall, error)
	ResolveShortfall(ctx context.Context, req ResolveShortfallRequest) (*domain.QuotaAddonShortfall, error)
}

// CatalogService fetches and refreshes plan definitions.
type CatalogService interface {
	GetPlan(ctx context.Context, planCode string) (*domain.Plan, error)
	RefreshPlan(ctx context.Context, planCode string) (*domain.Plan, error)
	FanoutCatalogRefresh(ctx context.Context, planCode string) error
	ListPlans(ctx context.Context) ([]string, error)
}

// TrialService manages trial profile assignments and trial-scoped overrides.
type TrialService interface {
	AssignTrialProfile(ctx context.Context, req AssignTrialProfileRequest) (*domain.TenantTrialAssignment, error)
	SetTrialOverride(ctx context.Context, req SetTrialOverrideRequest) (*domain.TrialEntitlementOverride, error)
	RemoveTrialOverride(ctx context.Context, req RemoveTrialOverrideRequest) error
	GetTrialAssignment(ctx context.Context, tenantID uuid.UUID) (*domain.TenantTrialAssignment, error)
	ListTrialOverrides(ctx context.Context, tenantID uuid.UUID) ([]domain.TrialEntitlementOverride, error)
	PurgeTrialForTenant(ctx context.Context, tenantID uuid.UUID) error
}

// ── Request types ──────────────────────────────────────────────────────────

// SetOverrideRequest contains parameters for creating or updating an override.
type SetOverrideRequest struct {
	TenantID  uuid.UUID
	LimitKey  domain.LimitKey
	Value     *int64 // nil = unlimited
	Reason    string
	SetBy     uuid.UUID
	ExpiresAt *time.Time
}

// RemoveOverrideRequest identifies which override to delete.
type RemoveOverrideRequest struct {
	TenantID  uuid.UUID
	LimitKey  domain.LimitKey
	RemovedBy uuid.UUID
	Reason    string
}

// AddAddonRequest contains parameters for granting a quota addon pack.
type AddAddonRequest struct {
	TenantID    uuid.UUID
	LimitKey    domain.LimitKey
	TotalTokens int64
	GrantedBy   uuid.UUID
	Reason      string
	ExpiresAt   *time.Time
}

// RemoveAddonRequest identifies which addon pack to revoke.
type RemoveAddonRequest struct {
	TenantID  uuid.UUID
	AddonID   uuid.UUID
	RemovedBy uuid.UUID
	Reason    string
}

// ApplyBillingPurchaseRequest carries the purchase parameters from Billing's
// TenantQuotaAddOnChanged {reason: "purchase"} event (E-1 / BL-OQ-4).
type ApplyBillingPurchaseRequest struct {
	TenantID    uuid.UUID
	Resource    string    // Billing short name, e.g. "llm_tokens"
	Delta       int64     // positive: tokens to grant
	BillingRef  string    // purchase_id — idempotency key
	PurchasedAt time.Time // payment-confirmation time; expiry = PurchasedAt + 12 months
}

// ApplyBillingRefundRequest carries the refund parameters from Billing's
// TenantQuotaAddOnChanged {reason: "refund"} event (C-5 / BL-OQ-23).
// Delta is always negative; PurchaseRef is Billing's purchase ID and matches
// billing_ref on the original grant — not the addon's internal UUID.
type ApplyBillingRefundRequest struct {
	TenantID    uuid.UUID
	PurchaseRef uuid.UUID
	Delta       int64
	BillingRef  uuid.UUID // Billing refund_id — the idempotency key (F-3)
}

// RecordDrawdownRequest deducts tokens from addon packs for a billing period.
type RecordDrawdownRequest struct {
	TenantID       uuid.UUID
	LimitKey       domain.LimitKey
	TokensConsumed int64
	PeriodStart    time.Time
	RequestID      *uuid.UUID // idempotency key (ENT-D11)
}

// ResolveShortfallRequest contains the operator's resolution decision.
type ResolveShortfallRequest struct {
	ShortfallID    uuid.UUID
	Disposition    domain.ShortfallDisposition
	DispositionBy  uuid.UUID
	DispositionRef *string
	Note           *string
}

// AssignTrialProfileRequest creates or replaces the trial assignment for a tenant.
type AssignTrialProfileRequest struct {
	TenantID uuid.UUID
	PlanCode string
	Profile  domain.TrialProfile
	StartsAt time.Time
	EndsAt   time.Time
	SetBy    uuid.UUID
	// Reason is required (CD-6, LLD §5.4 ENT-O3) and recorded in the AL-5 entry.
	Reason string
}

// SetTrialOverrideRequest creates or replaces a trial-scoped limit override.
type SetTrialOverrideRequest struct {
	TenantID uuid.UUID
	LimitKey domain.LimitKey
	Value    *int64 // nil = unlimited
	SetBy    uuid.UUID
}

// RemoveTrialOverrideRequest removes a trial-scoped limit override.
type RemoveTrialOverrideRequest struct {
	TenantID uuid.UUID
	LimitKey domain.LimitKey
}
