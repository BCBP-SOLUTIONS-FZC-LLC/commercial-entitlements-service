package sqspipeline

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

// InstallTracingHooks wires the port-level OTel hooks — trace extractor,
// span context extractor, and consumer span factory — using gincommon's
// registered TracerProvider. Must be called after gincommon.InitTracingWithConfig.
func InstallTracingHooks(serviceName string) {
	port.SetTraceExtractor(gincommon.SpanTraceID)

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
