package domain

import (
	"time"

	"github.com/google/uuid"
)

// QuotaAddonStatus tracks the lifecycle of a quota addon pack.
type QuotaAddonStatus string

const (
	AddonStatusActive   QuotaAddonStatus = "active"
	AddonStatusExpired  QuotaAddonStatus = "expired"
	AddonStatusConsumed QuotaAddonStatus = "consumed"
	AddonStatusRevoked  QuotaAddonStatus = "revoked"
	// AddonStatusRefunded: Billing refunded the whole purchased quantity
	// (refunded tokens == TotalTokens); zero effective balance (ENT-D16).
	AddonStatusRefunded QuotaAddonStatus = "refunded"
	// AddonStatusForfeited: the tenant was purged and the unused balance was
	// forfeited without refund (CD-55; HLD v0.69 P-5a for an unconverted
	// trial). The row is kept for the drawdown/refund ledgers (E-9).
	AddonStatusForfeited QuotaAddonStatus = "forfeited"
)

// QuotaAddon represents an additional quota allocation added on top of the plan.
//
// Quantities (ENT-D16):
//   - TotalTokens is the original purchased or granted quantity. It is
//     immutable: refunds never reduce it.
//   - RemainingTokens is the effective, still-usable balance; drawdowns and
//     refunds both reduce it.
//   - RefundedTokens is the sum of applied refunds, read from the
//     quota_addon_refunds ledger (the system of record). It is not a column
//     and is populated only where a caller needs it (listings, removal).
//   - Consumed (drawn) = TotalTokens − RemainingTokens − RefundedTokens.
type QuotaAddon struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	LimitKey        LimitKey
	TotalTokens     int64
	RemainingTokens int64
	RefundedTokens  int64
	GrantedBy       uuid.UUID
	Reason          string
	// BillingRef is the Billing-side idempotency key set only for packs created
	// from TenantQuotaAddOnChanged {reason: "purchase"} (E-1 / BL-OQ-4).
	// Nil for operator-created packs.
	BillingRef    *string
	Status        QuotaAddonStatus
	ExpiresAt     *time.Time
	RecordVersion int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ConsumedTokens is the quantity drawn by usage. It is meaningful while the
// pack is active or refunded; expiry, revocation and purge forfeiture zero
// RemainingTokens, so for expired, revoked and forfeited packs it also includes
// the forfeited balance.
func (a *QuotaAddon) ConsumedTokens() int64 {
	c := a.TotalTokens - a.RemainingTokens - a.RefundedTokens
	if c < 0 {
		return 0
	}
	return c
}

// Removable reports whether an operator may revoke the pack (ENT-O5 DELETE):
// it must be active and nothing may have been drawn from it. Refunded tokens
// do not count as use, so a partly refunded, undrawn pack is removable.
// Requires RefundedTokens to be populated.
func (a *QuotaAddon) Removable() bool {
	return a.Status == AddonStatusActive && a.ConsumedTokens() == 0
}

// QuotaAddonRefund records one applied Billing add-on refund (F-3,
// BL-OQ-20 W-5). BillingRef is Billing's refund_id and is the idempotency key:
// a refund is applied at most once, however often it is delivered.
// TokensRemoved is the actual reduction, 0 when nothing was left unused.
type QuotaAddonRefund struct {
	BillingRef    uuid.UUID
	TenantID      uuid.UUID
	AddonID       uuid.UUID
	PurchaseRef   uuid.UUID
	TokensRemoved int64
	AppliedAt     time.Time
}

// QuotaAddonDrawdown records a single usage deduction from a QuotaAddon.
type QuotaAddonDrawdown struct {
	ID             uuid.UUID
	AddonID        uuid.UUID
	TenantID       uuid.UUID
	LimitKey       LimitKey
	TokensConsumed int64
	PeriodStart    time.Time
	RequestID      *uuid.UUID // idempotency key
	RecordedAt     time.Time
}

// PackDraw summarises how much was drawn from each addon pack for a drawdown
// request (ENT-D11 auto-accept uses this to determine if shortfall occurred).
type PackDraw struct {
	AddonID uuid.UUID
	Drawn   int64
}
