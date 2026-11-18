package port

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ── Transaction context ───────────────────────────────────────────────────────

// txKey is the unexported context key for pgx.Tx.
type txKey struct{}

// WithTx stores the active pgx.Tx in ctx so repository helpers and
// EventPublisher.Enqueue join the same transaction.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext retrieves the active pgx.Tx set by WithTx, if any.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// ── EventPublisher context ────────────────────────────────────────────────────

type contextPublisherKey struct{}

// EventPublisherFromContext retrieves the publisher injected by TxRunner.RunInTx.
func EventPublisherFromContext(ctx context.Context) (EventPublisher, bool) {
	p, ok := ctx.Value(contextPublisherKey{}).(EventPublisher)
	return p, ok
}

// WithEventPublisher stores a publisher in ctx. Production TxRunner injects
// the eventbus Publisher after attaching the running tx; tests inject fakes.
func WithEventPublisher(ctx context.Context, p EventPublisher) context.Context {
	return context.WithValue(ctx, contextPublisherKey{}, p)
}

// ── AuditRecorder context ─────────────────────────────────────────────────────

type contextAuditKey struct{}

// AuditRecorderFromContext retrieves the audit recorder injected by TxRunner.RunInTx.
func AuditRecorderFromContext(ctx context.Context) (AuditRecorder, bool) {
	r, ok := ctx.Value(contextAuditKey{}).(AuditRecorder)
	return r, ok
}

// WithAuditRecorder stores an audit recorder in ctx.
func WithAuditRecorder(ctx context.Context, r AuditRecorder) context.Context {
	return context.WithValue(ctx, contextAuditKey{}, r)
}

// ── SpanContext / OTel ────────────────────────────────────────────────────────

// SpanContext carries trace and span IDs for correlation in outbox envelopes.
type SpanContext struct {
	TraceID string
	SpanID  string
}

// spanContextExtractor is set once by cmd/server/main.go after OTel init.
var spanContextExtractor func(ctx context.Context) SpanContext

// SetSpanContextExtractor installs the OTel-backed extractor. Called once by
// cmd/server/main.go after gincommon.InitTracingWithConfig.
func SetSpanContextExtractor(fn func(ctx context.Context) SpanContext) {
	spanContextExtractor = fn
}

// SpanContextFromContext extracts trace/span IDs using the installed extractor.
// Returns empty SpanContext when no extractor is installed (tests, reconciler).
func SpanContextFromContext(ctx context.Context) SpanContext {
	if spanContextExtractor == nil {
		return SpanContext{}
	}
	return spanContextExtractor(ctx)
}

// ── Consumer span ─────────────────────────────────────────────────────────────

// ConsumerSpanFunc wraps an SQS handler invocation in an OTel span.
type ConsumerSpanFunc func(ctx context.Context, name, queue string, link SpanContext) (context.Context, func(error))

var consumerSpanFn ConsumerSpanFunc

// SetConsumerSpanFunc installs the OTel consumer-span wrapper.
func SetConsumerSpanFunc(fn ConsumerSpanFunc) {
	consumerSpanFn = fn
}

// WrapConsumerSpan starts an OTel consumer span linked to the publisher span.
// When no ConsumerSpanFunc is installed, ctx is returned unchanged and end is a no-op.
func WrapConsumerSpan(ctx context.Context, name, queue string, link SpanContext) (context.Context, func(error)) {
	if consumerSpanFn == nil {
		return ctx, func(error) {}
	}
	return consumerSpanFn(ctx, name, queue, link)
}
