// Package port_test holds in-package tests for cases NOT covered by
// test/unit/context_test.go. Run with: go test ./internal/core/port/...
package port_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	. "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// TestWithEventPublisher_ChildContextInherits verifies that a context.WithValue
// child of a publisher-enriched context still carries the publisher — test/unit
// only tests the direct round-trip.
func TestWithEventPublisher_ChildContextInherits(t *testing.T) {
	pub := &fakePublisher{}
	parent := WithEventPublisher(context.Background(), pub)
	child := context.WithValue(parent, struct{ k string }{"extra"}, "v")
	got, ok := EventPublisherFromContext(child)
	require.True(t, ok, "child context must inherit the publisher from its parent")
	assert.Equal(t, pub, got)
}

// TestSpanContextFromContext_ExtractorRemoved verifies that clearing the
// extractor (SetSpanContextExtractor(nil)) after it was set reverts to the
// empty SpanContext — test/unit only tests install, not removal.
func TestSpanContextFromContext_ExtractorRemoved(t *testing.T) {
	t.Cleanup(func() { SetSpanContextExtractor(nil) })

	SetSpanContextExtractor(func(_ context.Context) SpanContext {
		return SpanContext{TraceID: "t", SpanID: "s"}
	})
	SetSpanContextExtractor(nil) // remove
	sc := SpanContextFromContext(context.Background())
	assert.Empty(t, sc.TraceID, "after removing the extractor SpanContext must be empty again")
	assert.Empty(t, sc.SpanID)
}

// TestWrapConsumerSpan_EndCallbackReceivesError verifies that the end
// callback installed by SetConsumerSpanFunc actually receives the error
// argument — test/unit only checks that the fn was called, not the callback.
func TestWrapConsumerSpan_EndCallbackReceivesError(t *testing.T) {
	t.Cleanup(func() { SetConsumerSpanFunc(nil) })

	sentinel := domain.NewError(domain.ErrRequestTimeout, "timeout")
	var receivedErr error
	SetConsumerSpanFunc(func(ctx context.Context, _, _ string, _ SpanContext) (context.Context, func(error)) {
		return ctx, func(err error) { receivedErr = err }
	})
	_, end := WrapConsumerSpan(context.Background(), "process", "my-queue", SpanContext{})
	end(sentinel)
	require.ErrorIs(t, receivedErr, domain.ErrRequestTimeout,
		"end callback must receive the error that was passed to it")
}

// TestWithAuditRecorder_ChildContextInherits mirrors the publisher child-context
// test but for AuditRecorder — ensures the value propagates through WithValue wrappers.
func TestWithAuditRecorder_ChildContextInherits(t *testing.T) {
	rec := &fakeAuditRecorder{}
	parent := WithAuditRecorder(context.Background(), rec)
	child := context.WithValue(parent, struct{ k string }{"other"}, "v")
	got, ok := AuditRecorderFromContext(child)
	require.True(t, ok)
	assert.Equal(t, rec, got)
}

// ── fakes ──────────────────────────────────────────────────────────────────

type fakePublisher struct{}

func (f *fakePublisher) Enqueue(_ context.Context, _ *domain.DomainEvent) error { return nil }

type fakeAuditRecorder struct{}

func (f *fakeAuditRecorder) Record(_ context.Context, _ AuditEntry) error        { return nil }
func (f *fakeAuditRecorder) RecordBatch(_ context.Context, _ []AuditEntry) error { return nil }
