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
// entitlement recomputation. It subscribes to iam.membership.events, which
// carries TenantMembershipsPurged and TenantStateChanged (EVT-16).
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
	case "TenantStateChanged":
		return processOnce(ctx, c.inbox, MembershipConsumerName, env, func(txCtx context.Context) error {
			return c.handleTenantStateChanged(txCtx, env)
		})
	default:
		return ackUnknown(ctx, c.inbox, c.log, MembershipConsumerName, env)
	}
}

func (c *MembershipConsumer) handlePurge(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundMembershipEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil //nolint:nilerr // ack malformed
	}
	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, domain.CauseTenantLifecycle)
	return err
}

// handleTenantStateChanged processes TenantStateChanged events from iam-org-membership.
// Only cause=="TrialReactivated" triggers a recompute; other causes are ack'd silently
// because they arrive via dedicated events on tenant-entitlements-q.
func (c *MembershipConsumer) handleTenantStateChanged(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundTenantStateChangedEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil //nolint:nilerr // ack malformed
	}
	if payload.Cause != "TrialReactivated" {
		return nil //nolint:nilerr
	}
	_, err := c.effectSvc.Recompute(ctx, payload.TenantID, domain.CauseTenantLifecycle)
	return err
}
