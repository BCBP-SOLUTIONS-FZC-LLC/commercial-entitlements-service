package consumer

import (
	"context"
	"encoding/json"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// CatalogConsumerName is the stable consumer name used for processed_events dedup.
const CatalogConsumerName = "catalog-entitlements-q"

// CatalogConsumer processes plan catalog change events from IAM.
// On receipt it refreshes the local plan cache and optionally triggers
// a Temporal fan-out for all tenants on that plan.
type CatalogConsumer struct {
	inbox      port.Inbox
	catalogSvc port.CatalogService
	cache      port.Cache
	log        port.Logger
}

// NewCatalogConsumer wires up the CatalogConsumer.
func NewCatalogConsumer(
	inbox port.Inbox,
	catalogSvc port.CatalogService,
	cache port.Cache,
	log port.Logger,
) *CatalogConsumer {
	return &CatalogConsumer{
		inbox:      inbox,
		catalogSvc: catalogSvc,
		cache:      cache,
		log:        log,
	}
}

// Handle dispatches the envelope to the appropriate handler.
func (c *CatalogConsumer) Handle(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	switch env.Type {
	case "PlanCatalogChanged":
		return processOnce(ctx, c.inbox, CatalogConsumerName, env, func(txCtx context.Context) error {
			return c.handleCatalogChanged(txCtx, env)
		})
	default:
		return ackUnknown(ctx, c.inbox, c.log, CatalogConsumerName, env)
	}
}

func (c *CatalogConsumer) handleCatalogChanged(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var payload domain.InboundCatalogEvent
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.log.Warn("catalog consumer: unmarshal failed",
			map[string]interface{}{"event_type": env.Type, "error": err.Error()})
		return nil
	}

	// Invalidate Valkey cache.
	if c.cache != nil {
		if err := c.cache.InvalidatePlan(ctx, payload.PlanCode); err != nil {
			c.log.Warn("catalog consumer: invalidate plan cache failed",
				map[string]interface{}{"plan_code": payload.PlanCode, "error": err.Error()})
		}
	}

	// Refresh plan from IAM.
	if _, err := c.catalogSvc.RefreshPlan(ctx, payload.PlanCode); err != nil {
		return err
	}

	// Fan-out recomputation to all tenants on this plan (async via Temporal).
	return c.catalogSvc.FanoutCatalogRefresh(ctx, payload.PlanCode)
}
