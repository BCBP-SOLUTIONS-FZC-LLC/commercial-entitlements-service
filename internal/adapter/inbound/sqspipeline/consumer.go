// Package sqspipeline wraps platform-events' SQS consumer so handlers in this
// service never import the SQS SDK directly. All wiring is in cmd/server/main.go.
package sqspipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	eventcfg "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// Handler builds a platform-events consumer handle (events.Handler) that:
//  1. Wraps the invocation in an OTel consumer span (port.WrapConsumerSpan).
//  2. Optionally validates the payload against the in-process schema registry.
//  3. Delegates to handlerFn.
//  4. Forwards rejected messages to the DLQ.
func Handler(
	queueName string,
	handlerFn func(ctx context.Context, env events.Envelope[json.RawMessage]) error,
	validator ValidatorFunc,
	dlq events.DLQPublisher,
	log port.Logger,
) events.Handler {
	return func(ctx context.Context, env events.Envelope[json.RawMessage]) error {
		// Validate payload against embedded schema (forward-compat: ErrNoSchema passes).
		if validator != nil {
			if verr := validator.Validate(env.Type, env.Payload); verr != nil && !errors.Is(verr, ErrNoSchema) {
				log.Warn("sqspipeline: schema validation failed — forwarding to DLQ",
					map[string]interface{}{"queue": queueName, "event_type": env.Type, "error": verr.Error()})
				if dlq != nil {
					src, ok := events.SourceMessageFromContext(ctx)
					if ok {
						_ = dlq.SendToDLQ(ctx, src.QueueURL, src.Body, src.Attributes, "schema_violation")
					}
				}
				return nil
			}
		}

		// Wrap in OTel consumer span using the publisher's trace/span IDs.
		link := port.SpanContext{
			TraceID: env.TraceID,
			SpanID:  env.CorrelationID,
		}
		spanName := fmt.Sprintf("consumer.%s", env.Type)
		ctx, end := port.WrapConsumerSpan(ctx, spanName, queueName, link)
		err := handlerFn(ctx, env)
		end(err)
		return err
	}
}

// ValidatorFunc validates an event payload against an in-process schema.
type ValidatorFunc interface {
	Validate(eventType string, payload []byte) error
}

// ErrNoSchema is returned when no schema is registered for an event type.
// Consumers forward-compat through this sentinel.
var ErrNoSchema = errors.New("no schema")

// NewConsumer creates a new platform-events SQS consumer.
func NewConsumer(
	env eventcfg.SQSConfigEnv,
	sqsClient events.SQSClientLike,
	handlerFn events.Handler,
	dlq events.DLQPublisher,
	log port.Logger,
) (events.Consumer, error) {
	opts := eventcfg.SQSConsumerOptions(env)
	if dlq != nil {
		opts = append(opts, events.WithDLQForwarding(dlq))
	}
	return events.NewSQSConsumerWithClient(eventcfg.SQSConfigFromEnv(env, log), sqsClient, handlerFn, opts...)
}

// EnvForQueue builds per-queue SQS config by overlaying the queue URL and
// concurrency onto the base SQS env config.
func EnvForQueue(base eventcfg.SQSConfigEnv, queueURL string, concurrency int) eventcfg.SQSConfigEnv {
	env := base
	env.QueueURL = queueURL
	if concurrency > 0 {
		env.Concurrency = concurrency
	}
	return env
}

// RequireDLQ verifies that the given queue has a DLQ configured and returns
// an error if not. This is a preflight check at startup.
func RequireDLQ(ctx context.Context, dlq events.DLQPublisher, queueURL string, log port.Logger) error {
	if dlq == nil {
		log.Warn("sqspipeline: no DLQ publisher wired — DLQ preflight skipped",
			map[string]interface{}{"queue_url": queueURL})
		return nil
	}
	dlqURL, err := dlq.ResolveDLQ(ctx, queueURL)
	switch {
	case err == nil:
		if log != nil {
			log.Info("DLQ forwarding wired", map[string]interface{}{"queue_url": queueURL, "dlq_url": dlqURL})
		}
		return nil
	case errors.Is(err, events.ErrDLQNotConfigured), errors.Is(err, events.ErrDLQInvalidRedrivePolicy):
		return fmt.Errorf("queue %s has no usable RedrivePolicy: %w", queueURL, err)
	default:
		log.Warn("could not resolve DLQ at startup; forwards will resolve it again",
			map[string]interface{}{"queue_url": queueURL, "error": err.Error()})
		return nil
	}
}
