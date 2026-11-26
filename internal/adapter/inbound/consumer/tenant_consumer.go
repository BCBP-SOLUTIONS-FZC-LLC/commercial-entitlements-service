package consumer

import (
	"context"
	"encoding/json"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// TenantConsumerName is the stable consumer name used for processed_events dedup.
const TenantConsumerName = "tenant-entitlements-q"

// TenantEventConsumer handles tenant lifecycle events that may require
// an entitlement recomputation or trial data purge.
type TenantEventConsumer struct {
	inbox     port.Inbox
	effectSvc port.EffectiveSetService
	trialSvc  port.TrialService
	log       port.Logger
}

// NewTenantEventConsumer wires up the TenantEventConsumer.
func NewTenantEventConsumer(
	inbox port.Inbox,
	effectSvc port.EffectiveSetService,
	trialSvc port.TrialService,
	log port.Logger,
) *TenantEventConsumer {
	return &TenantEventConsumer{
		inbox:     inbox,
		effectSvc: effectSvc,
		trialSvc:  trialSvc,
		log:       log,
	}
}

// Handle dispatches the envelope to the appropriate handler.
func (c *TenantEventConsumer) Handle(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	switch env.Type {
	case "TenantIdentityProvisioned", "TenantSubscriptionUpdated":
		return processOnce(ctx, c.inbox, TenantConsumerName, env, func(txCtx context.Context) error {
			return c.handleLifecycleEvent(txCtx, env)
		})
	case "TenantMembershipsPurged", "TenantConverted", "TenantOffboarded":
		return processOnce(ctx, c.inbox, TenantConsumerName, env, func(txCtx context.Context) error {
			return c.handleOffboard(txCtx, env)
		})
	default:
		return ackUnknown(ctx, c.inbox, c.log, TenantConsumerName, env)
	}
}

func (c *TenantEventConsumer) handleLifecycleEvent(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundTenantEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Warn("tenant consumer: unmarshal failed",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return nil //nolint:nilerr
	}
	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, domain.CauseTenantLifecycle)
	return err
}

func (c *TenantEventConsumer) handleOffboard(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundMembershipEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Warn("tenant consumer: unmarshal failed for offboard event",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return nil //nolint:nilerr
	}
	if c.trialSvc != nil {
		if err := c.trialSvc.PurgeTrialForTenant(ctx, payload.TenantID); err != nil {
			c.log.Warn("tenant consumer: purge trial failed",
				map[string]interface{}{"tenant_id": payload.TenantID, "error": err.Error()})
		}
	}
	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, domain.CauseTenantLifecycle)
	return err
}
