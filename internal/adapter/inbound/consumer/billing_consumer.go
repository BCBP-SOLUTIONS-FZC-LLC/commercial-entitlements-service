// Package consumer implements the inbound SQS event consumers for the
// Commercial Entitlements service. Each consumer follows the reference
// pattern from iam-org-membership: processOnce() (platform-events inbox) +
// ackUnknown() for forward compatibility.
package consumer

import (
	"context"
	"encoding/json"

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
	case "BillingSubscriptionActivated",
		"BillingSubscriptionUpdated",
		"BillingSubscriptionSuspended",
		"BillingSubscriptionReactivated",
		"BillingTrialStarted",
		"BillingTrialExpired":
		return processOnce(ctx, c.inbox, BillingConsumerName, env, func(txCtx context.Context) error {
			return c.handleSubscriptionEvent(txCtx, env)
		})
	case "TenantQuotaAddOnChanged":
		return processOnce(ctx, c.inbox, BillingConsumerName, env, func(txCtx context.Context) error {
			return c.handleAddonRefund(txCtx, env)
		})
	default:
		return ackUnknown(ctx, c.inbox, c.log, BillingConsumerName, env)
	}
}

// handleAddonRefund processes TenantQuotaAddOnChanged {reason: "refund"} from Billing.
// Non-refund reasons are silently acknowledged (forward-compat).
func (c *BillingEventConsumer) handleAddonRefund(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundBillingAddonRefundEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Warn("billing consumer: addon event unmarshal failed",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return nil
	}
	if payload.Reason != "refund" {
		c.log.Info("billing consumer: non-refund addon event acknowledged",
			map[string]interface{}{"reason": payload.Reason, "event_id": env.ID})
		return nil
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
	if env.Type == "BillingTrialStarted" || env.Type == "BillingTrialExpired" {
		cause = domain.CauseTrialChange
	}

	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, cause)
	return err
}
