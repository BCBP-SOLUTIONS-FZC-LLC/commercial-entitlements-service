package unit_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── EventPublisher context ─────────────────────────────────────────────────

type fakePublisherForPort struct {
	calls int
}

func (f *fakePublisherForPort) Enqueue(_ context.Context, _ *domain.DomainEvent) error {
	f.calls++
	return nil
}

func TestWithEventPublisher_RoundTrip(t *testing.T) {
	pub := &fakePublisherForPort{}
	ctx := port.WithEventPublisher(context.Background(), pub)

	got, ok := port.EventPublisherFromContext(ctx)
	require.True(t, ok)
	require.NotNil(t, got)

	err := got.Enqueue(ctx, &domain.DomainEvent{Type: "test"})
	require.NoError(t, err)
	assert.Equal(t, 1, pub.calls)
}

func TestEventPublisherFromContext_MissingReturnsFalse(t *testing.T) {
	_, ok := port.EventPublisherFromContext(context.Background())
	assert.False(t, ok)
}

// ── AuditRecorder context ─────────────────────────────────────────────────

type fakeAuditRecorder struct {
	mu      sync.Mutex
	entries []port.AuditEntry
	err     error
}

func (f *fakeAuditRecorder) Record(_ context.Context, e port.AuditEntry) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	f.entries = append(f.entries, e)
	f.mu.Unlock()
	return nil
}

func (f *fakeAuditRecorder) RecordBatch(_ context.Context, entries []port.AuditEntry) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	f.entries = append(f.entries, entries...)
	f.mu.Unlock()
	return nil
}

func (f *fakeAuditRecorder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

func TestWithAuditRecorder_RoundTrip(t *testing.T) {
	rec := &fakeAuditRecorder{}
	ctx := port.WithAuditRecorder(context.Background(), rec)

	got, ok := port.AuditRecorderFromContext(ctx)
	require.True(t, ok)
	require.NotNil(t, got)

	err := got.Record(ctx, port.AuditEntry{EntryType: "test"})
	require.NoError(t, err)
	assert.Equal(t, 1, rec.callCount())
}

func TestAuditRecorderFromContext_MissingReturnsFalse(t *testing.T) {
	_, ok := port.AuditRecorderFromContext(context.Background())
	assert.False(t, ok)
}

// ── SpanContext ────────────────────────────────────────────────────────────

func TestSpanContextFromContext_NoExtractor_ReturnsEmpty(t *testing.T) {
	port.SetSpanContextExtractor(nil)
	sc := port.SpanContextFromContext(context.Background())
	assert.Empty(t, sc.TraceID)
	assert.Empty(t, sc.SpanID)
}

func TestSpanContextFromContext_WithExtractor(t *testing.T) {
	port.SetSpanContextExtractor(func(_ context.Context) port.SpanContext {
		return port.SpanContext{TraceID: "trace-123", SpanID: "span-456"}
	})
	t.Cleanup(func() { port.SetSpanContextExtractor(nil) })

	sc := port.SpanContextFromContext(context.Background())
	assert.Equal(t, "trace-123", sc.TraceID)
	assert.Equal(t, "span-456", sc.SpanID)
}

// ── ConsumerSpan ───────────────────────────────────────────────────────────

func TestWrapConsumerSpan_NoFn_NoOp(t *testing.T) {
	port.SetConsumerSpanFunc(nil)
	ctx, end := port.WrapConsumerSpan(context.Background(), "billing", "billing.events", port.SpanContext{})
	require.NotNil(t, ctx)
	require.NotNil(t, end)
	end(nil) // should not panic
}

func TestWrapConsumerSpan_WithFn_Called(t *testing.T) {
	var capturedName string
	port.SetConsumerSpanFunc(func(ctx context.Context, name, queue string, link port.SpanContext) (context.Context, func(error)) {
		capturedName = name
		return ctx, func(error) {}
	})
	t.Cleanup(func() { port.SetConsumerSpanFunc(nil) })

	ctx, end := port.WrapConsumerSpan(context.Background(), "catalog", "catalog.events", port.SpanContext{})
	require.NotNil(t, ctx)
	end(nil)
	assert.Equal(t, "catalog", capturedName)
}
