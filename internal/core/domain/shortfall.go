package domain

import (
	"time"

	"github.com/google/uuid"
)

// ShortfallStatus tracks the resolution lifecycle of a shortfall.
type ShortfallStatus string

const (
	ShortfallStatusPending         ShortfallStatus = "pending"
	ShortfallStatusAutoAccepted    ShortfallStatus = "auto_accepted"
	ShortfallStatusPendingApproval ShortfallStatus = "pending_approval"
	ShortfallStatusApproved        ShortfallStatus = "approved"
	ShortfallStatusRejected        ShortfallStatus = "rejected"
	ShortfallStatusExpired         ShortfallStatus = "expired"
)

// ShortfallDisposition is the resolution outcome chosen by an operator.
type ShortfallDisposition string

const (
	DispositionApprove ShortfallDisposition = "approve"
	DispositionReject  ShortfallDisposition = "reject"
)

// ValidShortfallStatuses is the approved vocabulary for the status query
// parameter on ENT-O6 (ListShortfalls). Handlers validate against this map
// before casting to ShortfallStatus so callers receive 400 for unknown values.
var ValidShortfallStatuses = map[ShortfallStatus]struct{}{
	ShortfallStatusPending:         {},
	ShortfallStatusAutoAccepted:    {},
	ShortfallStatusPendingApproval: {},
	ShortfallStatusApproved:        {},
	ShortfallStatusRejected:        {},
	ShortfallStatusExpired:         {},
}

// ValidShortfallDispositions is the approved vocabulary for the disposition
// field on ENT-O7 (ResolveShortfall). Handlers validate against this map.
var ValidShortfallDispositions = map[ShortfallDisposition]struct{}{
	DispositionApprove: {},
	DispositionReject:  {},
}

// QuotaAddonShortfall is raised when a drawdown request arrives and the tenant
// has insufficient addon quota balance (ENT-D11/D12).
type QuotaAddonShortfall struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	DrawdownID       uuid.UUID
	LimitKey         LimitKey
	Requested        int64
	Available        int64
	Shortfall        int64
	Status           ShortfallStatus
	Disposition      *ShortfallDisposition
	DispositionBy    *uuid.UUID
	DispositionRef   *string
	DispositionNote  *string
	AutoAcceptPolicy *ShortfallAutoAcceptPolicy
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ResolvedAt       *time.Time
}

// ShortfallAutoAcceptPolicy configures auto-escalation thresholds (ENT-D12).
type ShortfallAutoAcceptPolicy struct {
	// MaxShortfallPercent: if shortfall/requested <= this, auto-accept.
	MaxShortfallPercent float64 `json:"max_shortfall_percent"`
	// MaxAbsoluteShortfall: if shortfall <= this (tokens), auto-accept.
	MaxAbsoluteShortfall int64 `json:"max_absolute_shortfall"`
	// MaxPerTenantPerWindowDays: escalate after N shortfalls in rolling window.
	MaxPerTenantPerWindowDays int `json:"max_per_tenant_per_window_days"`
	WindowDays                int `json:"window_days"`
}
