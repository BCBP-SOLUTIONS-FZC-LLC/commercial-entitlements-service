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

// InboundBillingAddonRefundEvent is the payload of TenantQuotaAddOnChanged {reason: "refund"}
// consumed from billing.events (BL-OQ-23 / C-5). Delta is always negative.
type InboundBillingAddonRefundEvent struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	Resource    string    `json:"resource"`
	Delta       int64     `json:"delta"`
	Reason      string    `json:"reason"`
	BillingRef  string    `json:"billing_ref"`
	PurchaseRef uuid.UUID `json:"purchase_ref"`
}

// InboundBillingAddonPurchaseEvent is the payload of TenantQuotaAddOnChanged {reason: "purchase"}
// consumed from billing.events (E-1 / BL-OQ-4). Delta is always positive.
// PurchasedAt is Billing's payment-confirmation time and anchors the 12-month expiry.
type InboundBillingAddonPurchaseEvent struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	Resource    string    `json:"resource"`
	Delta       int64     `json:"delta"`
	Reason      string    `json:"reason"`
	BillingRef  string    `json:"billing_ref"`
	PurchasedAt time.Time `json:"purchased_at"`
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
