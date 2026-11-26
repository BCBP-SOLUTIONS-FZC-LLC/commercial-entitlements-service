package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/outbox"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// EventSource is the envelope Source every event this service produces carries.
const EventSource = "commercial-entitlements"

// Publisher implements port.EventPublisher. It JSON-marshals the event payload,
// validates it via the configured Codec (schema validation only — no wire
// encoding), wraps it as plain JSON in an events.Envelope, and inserts it into
// outbox_events on the transaction attached to ctx (EVT-10).
type Publisher struct {
	source string
	codec  Codec
	log    port.Logger
}

// New creates a Publisher.
func New(source string, codec Codec) *Publisher {
	return &Publisher{source: source, codec: codec}
}

// WithLogger injects a logger.
func (p *Publisher) WithLogger(log port.Logger) *Publisher {
	p.log = log
	return p
}

var _ port.EventPublisher = (*Publisher)(nil)

// Enqueue validates and writes one plain-JSON envelope into outbox_events.
func (p *Publisher) Enqueue(ctx context.Context, event *domain.DomainEvent) error {
	raw, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	// Schema validation only. ValidatingCodec validates against the embedded
	// JSON schema for event.Type (ErrNoSchema → forward-compat pass-through);
	// NoopCodec always passes. Genuine schema violations are hard errors.
	if _, _, err := p.codec.Encode(ctx, event.Type, raw); err != nil && !isNoSchemaErr(err) {
		return fmt.Errorf("validate event %s: %w", event.Type, err)
	}

	// Build envelope opts.
	var opts []events.EnvelopeOpt
	if event.TenantID != (domain.UUID{}) {
		opts = append(opts, events.WithTenantID(event.TenantID.String()))
	}
	if event.Subject != "" {
		opts = append(opts, events.WithSubject(event.Subject))
	}
	if event.Actor != "" {
		opts = append(opts, events.WithActor(event.Actor))
	}
	if event.IPAddress != "" {
		opts = append(opts, events.WithIPAddress(event.IPAddress))
	}
	if event.UserAgent != "" {
		opts = append(opts, events.WithUserAgent(event.UserAgent))
	}
	// OTel correlation.
	if sc := port.SpanContextFromContext(ctx); sc.TraceID != "" {
		opts = append(opts, events.WithTraceID(sc.TraceID))
		if sc.SpanID != "" {
			opts = append(opts, events.WithCorrelationID(sc.SpanID))
		}
	}

	env := events.NewEnvelope(event.Type, p.source, json.RawMessage(raw), opts...)

	if p.log != nil {
		p.log.Debug("outbox.Enqueue", map[string]interface{}{"type": event.Type, "tenant_id": event.TenantID})
	}

	tx, ok := port.TxFromContext(ctx)
	if !ok {
		return errors.New("event enqueue requires an open RunInTx transaction")
	}
	return outbox.Enqueue(ctx, tx, env)
}

// UUID is the uuid.UUID type aliased for use in key builders.
type UUID = interface{ String() string }

// NoopPublisher is an events.Publisher that silently discards all messages.
// Use in environments where no SNS topic is configured (dev, test, reconciler).
type NoopPublisher struct{}

// Publish discards the envelope.
func (NoopPublisher) Publish(_ context.Context, _ events.Envelope[json.RawMessage]) error {
	return nil
}

// PublishBatch discards the batch.
func (NoopPublisher) PublishBatch(_ context.Context, _ []events.Envelope[json.RawMessage]) error {
	return nil
}

// isNoSchemaErr reports whether err signals that no schema is registered for
// the event type. ValidatingCodec returns ErrNoSchema for forward-compat event
// types with no embedded schema; callers treat this as a pass-through, not a
// hard validation error.
func isNoSchemaErr(err error) bool {
	return errors.Is(err, ErrNoSchema)
}
