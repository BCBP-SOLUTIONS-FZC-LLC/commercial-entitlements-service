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
)

// QuotaAddon represents an additional quota allocation added on top of the plan.
type QuotaAddon struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	LimitKey        LimitKey
	TotalTokens     int64
	RemainingTokens int64
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
