package consumer

import (
	"context"
	"encoding/json"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// MembershipConsumerName is the stable consumer name used for processed_events dedup.
const MembershipConsumerName = "membership-entitlements-q"

// MembershipConsumer handles IAM membership lifecycle events that may require
// entitlement recomputation (e.g. TenantConverted → state changes).
type MembershipConsumer struct {
	inbox     port.Inbox
	effectSvc port.EffectiveSetService
	trialSvc  port.TrialService
	log       port.Logger
}

// NewMembershipConsumer wires up the MembershipConsumer.
func NewMembershipConsumer(
	inbox port.Inbox,
	effectSvc port.EffectiveSetService,
	trialSvc port.TrialService,
	log port.Logger,
) *MembershipConsumer {
	return &MembershipConsumer{
		inbox:     inbox,
		effectSvc: effectSvc,
		trialSvc:  trialSvc,
		log:       log,
	}
}

// Handle dispatches the envelope to the appropriate handler.
func (c *MembershipConsumer) Handle(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	switch env.Type {
	case "TenantMembershipsPurged":
		return processOnce(ctx, c.inbox, MembershipConsumerName, env, func(txCtx context.Context) error {
			return c.handlePurge(txCtx, env)
		})
	case "TenantConverted":
		return processOnce(ctx, c.inbox, MembershipConsumerName, env, func(txCtx context.Context) error {
			return c.handleConverted(txCtx, env)
		})
	case "TenantOffboarded":
		return processOnce(ctx, c.inbox, MembershipConsumerName, env, func(txCtx context.Context) error {
			return c.handleOffboarded(txCtx, env)
		})
	default:
		return ackUnknown(ctx, c.inbox, c.log, MembershipConsumerName, env)
	}
}

func (c *MembershipConsumer) handlePurge(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundMembershipEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil // ack malformed
	}
	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, domain.CauseTenantLifecycle)
	return err
}

func (c *MembershipConsumer) handleConverted(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundMembershipEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil
	}
	// Trial data is no longer relevant after conversion.
	if c.trialSvc != nil {
		if err := c.trialSvc.PurgeTrialForTenant(ctx, payload.TenantID); err != nil {
			c.log.Warn("membership consumer: purge trial on convert failed",
				map[string]interface{}{"tenant_id": payload.TenantID, "error": err.Error()})
		}
	}
	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, domain.CauseTenantLifecycle)
	return err
}

func (c *MembershipConsumer) handleOffboarded(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundMembershipEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil
	}
	if c.trialSvc != nil {
		if err := c.trialSvc.PurgeTrialForTenant(ctx, payload.TenantID); err != nil {
			c.log.Warn("membership consumer: purge trial on offboard failed",
				map[string]interface{}{"tenant_id": payload.TenantID, "error": err.Error()})
		}
	}
	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, domain.CauseTenantLifecycle)
	return err
}
