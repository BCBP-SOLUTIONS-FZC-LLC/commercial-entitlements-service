// Package consumer implements the inbound SQS event consumers for the
// Commercial Entitlements service. Each consumer follows the reference
// pattern from iam-org-membership: processOnce() (platform-events inbox) +
// ackUnknown() for forward compatibility.
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// BillingConsumerName is the stable consumer name used for processed_events dedup.
const BillingConsumerName = "billing-entitlements-q"

// BillingEventConsumer handles billing subscription and addon events.
type BillingEventConsumer struct {
	inbox     port.Inbox
	effectSvc port.EffectiveSetService
	trialSvc  port.TrialService
	addonSvc  port.AddonService
	log       port.Logger
}

// NewBillingEventConsumer wires up the BillingEventConsumer.
func NewBillingEventConsumer(
	inbox port.Inbox,
	effectSvc port.EffectiveSetService,
	trialSvc port.TrialService,
	addonSvc port.AddonService,
	log port.Logger,
) *BillingEventConsumer {
	return &BillingEventConsumer{
		inbox:     inbox,
		effectSvc: effectSvc,
		trialSvc:  trialSvc,
		addonSvc:  addonSvc,
		log:       log,
	}
}

// Handle dispatches the envelope to the appropriate handler.
func (c *BillingEventConsumer) Handle(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	switch env.Type {
	case "TenantPlanChanged",
		"TenantSubscriptionCancelled",
		"TenantReactivated":
		return processOnce(ctx, c.inbox, BillingConsumerName, env, func(txCtx context.Context) error {
			return c.handleSubscriptionEvent(txCtx, env)
		})
	case "TenantQuotaAddOnChanged":
		return processOnce(ctx, c.inbox, BillingConsumerName, env, func(txCtx context.Context) error {
			return c.handleAddonChanged(txCtx, env)
		})
	default:
		return ackUnknown(ctx, c.inbox, c.log, BillingConsumerName, env)
	}
}

// handleAddonChanged routes TenantQuotaAddOnChanged to the purchase or refund
// handler based on the reason field (E-1 / BL-OQ-4, C-5 / BL-OQ-20).
// The consumed schema limits reason to purchase|refund, so in the wired
// pipeline an unknown reason is dead-lettered by sqspipeline.Handler before it
// gets here (B-4); the default branch only acks when no validator is wired.
func (c *BillingEventConsumer) handleAddonChanged(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	// Peek at the reason before full deserialisation.
	var peek struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(env.Payload, &peek); err != nil {
		c.log.Error("billing consumer: addon event unmarshal failed — sending to DLQ",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return fmt.Errorf("billing consumer: addon event unmarshal failed: %w", err)
	}
	switch peek.Reason {
	case "purchase":
		return c.handleAddonPurchase(ctx, env)
	case "refund":
		return c.handleAddonRefund(ctx, env)
	default:
		c.log.Info("billing consumer: unknown addon reason acknowledged",
			map[string]interface{}{"reason": peek.Reason, "event_id": env.ID})
		return nil //nolint:nilerr
	}
}

// handleAddonPurchase processes TenantQuotaAddOnChanged {reason: "purchase"}.
// Creates a new addon pack; idempotent on billing_ref; expiry = purchased_at + 12 months.
func (c *BillingEventConsumer) handleAddonPurchase(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundBillingAddonPurchaseEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Error("billing consumer: addon purchase unmarshal failed — sending to DLQ",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return fmt.Errorf("billing consumer: addon purchase unmarshal failed: %w", err)
	}
	if payload.Delta <= 0 {
		c.log.Warn("billing consumer: purchase event has non-positive delta, skipping",
			map[string]interface{}{"delta": payload.Delta, "billing_ref": payload.BillingRef})
		return nil //nolint:nilerr
	}
	err := c.addonSvc.ApplyBillingPurchase(ctx, port.ApplyBillingPurchaseRequest{
		TenantID:    payload.TenantID,
		Resource:    payload.Resource,
		Delta:       payload.Delta,
		BillingRef:  payload.BillingRef,
		PurchasedAt: payload.PurchasedAt,
	})
	if errors.Is(err, domain.ErrBillingRefAlreadyClaimed) {
		// B-8: the billing_ref already belongs to another tenant's pack — a
		// duplicate or conflicting paid grant. Nothing was granted. Count it
		// (drives CommercialEntitlementsBillingRefConflict) and ack: a retry
		// cannot succeed, and the DLQ would hide a money signal.
		metrics.IncBillingRefConflict(env.Type, payload.Reason)
		c.log.Error("billing consumer: billing_ref already claimed by another tenant — add-on NOT granted; operator review required",
			map[string]interface{}{"event_id": env.ID, "billing_ref": payload.BillingRef, "tenant_id": payload.TenantID})
		return nil
	}
	return err
}

// handleAddonRefund processes TenantQuotaAddOnChanged {reason: "refund"} from Billing.
func (c *BillingEventConsumer) handleAddonRefund(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundBillingAddonRefundEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Error("billing consumer: addon refund unmarshal failed — sending to DLQ",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return fmt.Errorf("billing consumer: addon refund unmarshal failed: %w", err)
	}
	if payload.Delta >= 0 {
		c.log.Warn("billing consumer: refund event has non-negative delta, skipping",
			map[string]interface{}{"delta": payload.Delta, "billing_ref": payload.BillingRef})
		return nil //nolint:nilerr
	}
	err := c.addonSvc.ApplyBillingRefund(ctx, port.ApplyBillingRefundRequest{
		TenantID:    payload.TenantID,
		PurchaseRef: payload.PurchaseRef,
		Delta:       payload.Delta,
		BillingRef:  payload.BillingRef,
	})
	if errors.Is(err, domain.ErrBillingRefAlreadyClaimed) {
		// F-3: this refund_id is already recorded for another tenant or for a
		// different purchase. Nothing was changed; a retry cannot succeed.
		// Count it (same alert as B-8) and ack.
		reason := domain.RefundConflictOtherTenant
		var de *domain.DomainError
		if errors.As(err, &de) {
			if k, ok := de.Details["conflict"].(string); ok && k != "" {
				reason = k
			}
		}
		metrics.IncBillingRefConflict(env.Type, reason)
		c.log.Error("billing consumer: refund billing_ref already recorded — refund NOT applied; operator review required",
			map[string]interface{}{"event_id": env.ID, "billing_ref": payload.BillingRef, "tenant_id": payload.TenantID,
				"purchase_ref": payload.PurchaseRef, "conflict": reason, "error": err.Error()})
		return nil
	}
	return err
}

// handleSubscriptionEvent handles Billing's TenantPlanChanged,
// TenantSubscriptionCancelled and TenantReactivated by recomputing from Core's
// I-17, not from the event. Core applies the same Billing event on its own
// queue and the two consumers are unordered, so this read can still see the
// old plan or status; the authoritative second recompute comes from Core's
// TenantStateChanged relay (MembershipConsumer, B-3).
//
// The payload is decoded into the type's own struct (B-5). A payload that
// does not decode, or has no tenant_id, is returned as an error so it is
// retried and dead-lettered instead of silently acked.
func (c *BillingEventConsumer) handleSubscriptionEvent(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	tenantID, err := billingSubscriptionTenant(env)
	if err != nil {
		c.log.Error("billing consumer: subscription event did not decode — sending to DLQ",
			map[string]interface{}{"event_type": env.Type, "event_id": env.ID, "error": err.Error()})
		return err
	}
	_, err = c.effectSvc.Recompute(ctx, tenantID, domain.CauseBillingEvent)
	return err
}

// billingSubscriptionTenant decodes a Billing subscription event into its own
// payload type and returns its tenant.
func billingSubscriptionTenant(env events.Envelope[json.RawMessage]) (uuid.UUID, error) {
	var (
		tenantID uuid.UUID
		err      error
	)
	switch env.Type {
	case "TenantPlanChanged":
		var p domain.InboundBillingPlanChangedEvent
		err = json.Unmarshal(env.Payload, &p)
		tenantID = p.TenantID
	case "TenantSubscriptionCancelled":
		var p domain.InboundBillingSubscriptionCancelledEvent
		err = json.Unmarshal(env.Payload, &p)
		tenantID = p.TenantID
	case "TenantReactivated":
		var p domain.InboundBillingReactivatedEvent
		err = json.Unmarshal(env.Payload, &p)
		tenantID = p.TenantID
	default:
		return uuid.Nil, fmt.Errorf("billing consumer: %q is not a subscription event", env.Type)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("billing consumer: %s unmarshal failed: %w", env.Type, err)
	}
	if tenantID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("billing consumer: %s has no tenant_id", env.Type)
	}
	return tenantID, nil
}
