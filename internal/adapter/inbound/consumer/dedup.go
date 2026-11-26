package consumer

import (
	"context"
	"encoding/json"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// knownEventTypes is the bounded vocabulary of event types handled by this
// service. Any type outside this set is capped to "unknown" in Prometheus
// labels to prevent cardinality explosions from rogue or misconfigured
// producers (CONS-1 / observability standard).
var knownEventTypes = func() map[string]struct{} {
	types := []string{
		// billing-entitlements-q (billing.events)
		"TenantPlanChanged", "TenantSubscriptionCancelled", "TenantReactivated",
		"TenantQuotaAddOnChanged",
		// tenant-entitlements-q (iam-tenant-events from iam-realm-provisioner)
		"TenantIdentityProvisioned", "TenantSubscriptionUpdated",
		"TenantSuspended", "TenantReactivated", "TenantOffboarded",
		"TenantConverted", "TenantMembershipsPurged",
		"TrialExpired", "TrialReactivated",
		// catalog-entitlements-q (iam-catalog-events)
		"PlanCatalogChanged",
		// membership-entitlements-q (iam-membership-events from iam-org-membership)
		"TenantMembershipsPurged", "TenantStateChanged",
	}
	m := make(map[string]struct{}, len(types))
	for _, t := range types {
		m[t] = struct{}{}
	}
	return m
}()

func sanitizeEventType(t string) string {
	if _, ok := knownEventTypes[t]; ok {
		return t
	}
	return "unknown"
}

// processOnce runs fn exactly once per (consumer, env.ID) through the
// platform-events inbox (port.Inbox): the processed_events claim and fn's
// writes commit in one transaction (IDEMP-1..4). A duplicate skips fn and
// increments commercial_entitlements_duplicate_messages_total{consumer,event_type},
// this repo's authoritative signal; platform-events inbox.Store also records
// platform_duplicate_messages_total{queue,event_type} as the Tier-1 signal.
func processOnce(ctx context.Context, inbox port.Inbox, consumer string, env events.Envelope[json.RawMessage], fn func(txCtx context.Context) error) error {
	if inbox == nil {
		// No inbox wired (reconciler jobs / tests); run fn directly.
		return fn(ctx)
	}
	duplicate, err := inbox.ProcessOnce(ctx, env.ID, env.Type, fn)
	if err != nil {
		return err
	}
	if duplicate {
		metrics.IncDuplicate(consumer, sanitizeEventType(env.Type))
	}
	return nil
}

// ackUnknown is the forward-compat path: an event type with no wired
// handler is logged, counted, and recorded in processed_events so
// redelivery does not storm the same unknown type.
func ackUnknown(ctx context.Context, inbox port.Inbox, log port.Logger, consumer string, env events.Envelope[json.RawMessage]) error {
	metrics.IncUnknownEventAcknowledged(consumer, sanitizeEventType(env.Type))
	log.Info("unknown event type — silently acknowledging",
		map[string]interface{}{"event_id": env.ID, "event_type": env.Type})
	return processOnce(ctx, inbox, consumer, env, func(context.Context) error { return nil })
}

// ProcessedEventsConsumers returns all consumer names for use in reconciler's
// ProcessedEventsPrune job.
func ProcessedEventsConsumers() []string {
	return []string{
		BillingConsumerName,
		TenantConsumerName,
		CatalogConsumerName,
		MembershipConsumerName,
	}
}
