// Package consumer implements the inbound SQS event consumers for the
// Commercial Entitlements service. Each consumer follows the reference
// pattern from iam-org-membership: processOnce() (platform-events inbox) +
// ackUnknown() for forward compatibility.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"

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
// Unknown reasons are forward-compat acknowledged.
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
		return nil
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
		return nil
	}
	return c.addonSvc.ApplyBillingPurchase(ctx, port.ApplyBillingPurchaseRequest{
		TenantID:    payload.TenantID,
		Resource:    payload.Resource,
		Delta:       payload.Delta,
		BillingRef:  payload.BillingRef,
		PurchasedAt: payload.PurchasedAt,
	})
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
		return nil
	}
	return c.addonSvc.ApplyBillingRefund(ctx, port.ApplyBillingRefundRequest{
		TenantID:    payload.TenantID,
		PurchaseRef: payload.PurchaseRef,
		Delta:       payload.Delta,
		BillingRef:  payload.BillingRef,
	})
}

func (c *BillingEventConsumer) handleSubscriptionEvent(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundBillingEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Warn("billing consumer: unmarshal failed",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return nil // ack malformed events to prevent DLQ storms
	}

	cause := domain.CauseBillingEvent
	if payload.TrialProfile != nil {
		cause = domain.CauseTrialChange
	}

	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, cause)
	return err
}
