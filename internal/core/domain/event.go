// Package domain holds the core entity types for the Commercial Entitlements service.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// UUID is an alias for uuid.UUID for use in domain code and adapter key builders.
type UUID = uuid.UUID

// EventTypeTenantEntitlementsChanged is the canonical event type emitted after
// every recomputation of a tenant's effective entitlement set.
const EventTypeTenantEntitlementsChanged = "tenant_entitlements_changed"

// EventTypeTenantQuotaAddOnChanged is emitted by Entitlements (and only by
// Entitlements) when a quota add-on pack is granted, revoked, or expires.
// Billing uses purchased_at as the 12-month expiry anchor (BL-OQ-4, BL-OQ-20).
const EventTypeTenantQuotaAddOnChanged = "tenant_quota_add_on_changed"

// TopicEntitlements is the SNS topic that carries entitlement events.
const TopicEntitlements = "entitlements"

// TopicForEvent returns the topic name for the given event type.
// Currently all events go to the single entitlements topic.
func TopicForEvent(_ string) string {
	return TopicEntitlements
}

// DomainEvent wraps a typed payload for transactional outbox enqueue.
// The eventbus adapter constructs the platform-events envelope from these fields.
// Field names follow the reference implementation (iam-org-membership).
type DomainEvent struct {
	// Type is the event type string, e.g. "tenant_entitlements_changed".
	Type string

	// Data is the JSON-serialisable body of the event (called Payload in LLD).
	Data any

	// TenantID is used as the tenant envelope field.
	TenantID uuid.UUID

	// Subject identifies the primary resource (e.g. tenant ID string).
	Subject string

	// Actor is the user/service that triggered the event.
	Actor string

	// IPAddress is set for user-initiated events (from HTTP request context).
	IPAddress string

	// UserAgent is set for user-initiated events (from HTTP request context).
	UserAgent string

	// OccurredAt is the wall-clock time the domain action completed.
	OccurredAt time.Time
}

// NewEvent constructs a DomainEvent with current time.
func NewEvent(eventType string, payload any) *DomainEvent {
	return &DomainEvent{
		Type:       eventType,
		Data:       payload,
		OccurredAt: time.Now().UTC(),
	}
}

// NewTenantEvent constructs a DomainEvent scoped to a specific tenant.
func NewTenantEvent(eventType string, tenantID uuid.UUID, payload any) *DomainEvent {
	ev := NewEvent(eventType, payload)
	ev.TenantID = tenantID
	ev.Subject = tenantID.String()
	return ev
}

// TenantEntitlementsChangedPayload is the SNS/SQS event payload emitted after
// every recomputation. Downstream services key off Cause to react selectively.
type TenantEntitlementsChangedPayload struct {
	TenantID        uuid.UUID         `json:"tenant_id"`
	Version         int64             `json:"version"`
	Cause           EffectiveSetCause `json:"cause"`
	ChangedKeys     []string          `json:"changed_keys"`
	PreviousVersion int64             `json:"previous_version"`
	ComputedAt      time.Time         `json:"computed_at"`
}

// TenantQuotaAddOnChangedPayload is the event body for EventTypeTenantQuotaAddOnChanged.
// Action is one of "granted" | "revoked" | "expired".
// PurchasedAt is the wall-clock time the add-on was originally granted; Billing
// uses it as the 12-month expiry basis regardless of when this event arrives (BL-OQ-4).
type TenantQuotaAddOnChangedPayload struct {
	TenantID    uuid.UUID  `json:"tenant_id"`
	AddonID     uuid.UUID  `json:"addon_id"`
	LimitKey    string     `json:"limit_key"`
	TotalTokens int64      `json:"total_tokens"`
	Reason      string     `json:"reason"`
	Action      string     `json:"action"`
	PurchasedAt time.Time  `json:"purchased_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// InboundBillingEvent represents a billing subscription event from upstream.
type InboundBillingEvent struct {
	TenantID          uuid.UUID `json:"tenant_id"`
	SubscriptionState string    `json:"subscription_state"`
	PlanCode          string    `json:"plan_code"`
	TrialProfile      *string   `json:"trial_profile,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
}

// InboundTenantEvent represents a tenant provisioning / lifecycle event.
type InboundTenantEvent struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
}

// InboundCatalogEvent represents a plan catalog refresh signal.
type InboundCatalogEvent struct {
	PlanCode      string    `json:"plan_code"`
	RecordVersion int64     `json:"record_version"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// InboundMembershipEvent represents membership lifecycle events from IAM.
type InboundMembershipEvent struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
}
