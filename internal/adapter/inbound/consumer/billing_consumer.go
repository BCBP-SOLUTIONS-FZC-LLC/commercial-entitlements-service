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

const billingConsumerName = "billing-entitlements-q"

// BillingEventConsumer handles billing subscription events and triggers
// entitlement recomputation.
type BillingEventConsumer struct {
	inbox     port.Inbox
	effectSvc port.EffectiveSetService
	trialSvc  port.TrialService
	log       port.Logger
}

// NewBillingEventConsumer wires up the BillingEventConsumer.
func NewBillingEventConsumer(
	inbox port.Inbox,
	effectSvc port.EffectiveSetService,
	trialSvc port.TrialService,
	log port.Logger,
) *BillingEventConsumer {
	return &BillingEventConsumer{
		inbox:     inbox,
		effectSvc: effectSvc,
		trialSvc:  trialSvc,
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
		return processOnce(ctx, c.inbox, billingConsumerName, env, func(txCtx context.Context) error {
			return c.handleSubscriptionEvent(txCtx, env)
		})
	default:
		return ackUnknown(ctx, c.inbox, c.log, billingConsumerName, env)
	}
}

func (c *BillingEventConsumer) handleSubscriptionEvent(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundBillingEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Warn("billing consumer: unmarshal failed",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return nil // ack malformed events to prevent DLQ storms
	}

	cause := domain.CauseBillingEvent
	if env.Type == "BillingTrialStarted" {
		cause = domain.CauseTrialChange
	}

	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, cause)
	return err
}
