package domain

import (
	"time"

	"github.com/google/uuid"
)

// OverrideAction records the mutation that created a history row.
type OverrideAction string

const (
	ActionSet     OverrideAction = "set"
	ActionRemoved OverrideAction = "removed"
	ActionExpired OverrideAction = "expired"
)

// Override is an operator-managed manual limit for a single tenant+key.
// A nil Value means unlimited for that key.
type Override struct {
	TenantID      uuid.UUID
	LimitKey      LimitKey
	Value         *int64 // nil = unlimited
	Reason        string
	SetBy         uuid.UUID
	ExpiresAt     *time.Time
	RecordVersion int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// OverrideHistory records every mutation to an Override row for audit purposes.
type OverrideHistory struct {
	ID        int64
	TenantID  uuid.UUID
	LimitKey  LimitKey
	OldValue  *int64
	NewValue  *int64
	Action    OverrideAction
	Reason    string
	ActorID   uuid.UUID
	CreatedAt time.Time
}
