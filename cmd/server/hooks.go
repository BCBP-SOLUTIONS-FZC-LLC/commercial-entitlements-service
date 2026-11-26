package main

// installOTelHooks wires the OTel-specific port hooks that sqspipeline cannot
// install itself (it may only import gincommon, not go.opentelemetry.io/otel
// directly). Called once after gincommon.InitTracingWithConfig.
//
// Two hooks are installed here:
//   - SpanContextExtractor: extracts (TraceID, SpanID) from an active span so
//     the outbox enqueues the upstream span reference alongside the event.
//   - ConsumerSpanFunc: wraps each SQS handler call in a child consumer span
//     and links it to the upstream producer span read from the event envelope.

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

func installOTelHooks(serviceName string) {
	port.SetSpanContextExtractor(func(ctx context.Context) port.SpanContext {
		sc := trace.SpanFromContext(ctx).SpanContext()
		if !sc.IsValid() {
			return port.SpanContext{}
		}
		return port.SpanContext{
			TraceID: sc.TraceID().String(),
			SpanID:  sc.SpanID().String(),
		}
	})

	tracer := gincommon.NewTracer(serviceName)
	port.SetConsumerSpanFunc(func(ctx context.Context, name, queue string, link port.SpanContext) (context.Context, func(error)) {
		spanOpts := []trace.SpanStartOption{
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(attribute.String("messaging.destination.name", queue)),
		}
		if link.TraceID != "" && link.SpanID != "" {
			tid, err1 := trace.TraceIDFromHex(link.TraceID)
			sid, err2 := trace.SpanIDFromHex(link.SpanID)
			if err1 == nil && err2 == nil {
				sc := trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: tid,
					SpanID:  sid,
					Remote:  true,
				})
				if sc.IsValid() {
					spanOpts = append(spanOpts, trace.WithLinks(trace.Link{SpanContext: sc}))
				}
			}
		}
		ctx, span := tracer.Start(ctx, name, spanOpts...)
		return ctx, func(err error) {
			if err != nil {
				span.RecordError(err)
			}
			span.End()
		}
	})
}
