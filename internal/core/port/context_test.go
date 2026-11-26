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

// TestNopLogger_AllMethods covers the four NopLogger methods.
// They are no-ops; this test simply verifies they don't panic when called.
func TestNopLogger_AllMethods(t *testing.T) {
	var l NopLogger
	assert.NotPanics(t, func() { l.Info("msg", nil) })
	assert.NotPanics(t, func() { l.Warn("msg", map[string]any{"k": "v"}) })
	assert.NotPanics(t, func() { l.Error("msg", nil) })
	assert.NotPanics(t, func() { l.Debug("msg", nil) })
}

// ── SetTraceExtractor ─────────────────────────────────────────────────────────

func TestSetTraceExtractor_InstalledFnIsUsedBySlogStyleLogger(t *testing.T) {
	t.Cleanup(func() { SetTraceExtractor(nil) })
	SetTraceExtractor(func(_ context.Context) string { return "trace-999" })

	captured := &captureLog{}
	sl := NewSlogStyleLogger(captured)
	sl.InfoContext(context.Background(), "hello", "k", "v")

	require.Len(t, captured.calls, 1)
	assert.Equal(t, "trace-999", captured.calls[0].fields["trace_id"],
		"trace_id must be appended when traceExtractorFn is installed")
}

func TestSetTraceExtractor_NilExtractor_NoTraceID(t *testing.T) {
	t.Cleanup(func() { SetTraceExtractor(nil) })
	SetTraceExtractor(nil)

	captured := &captureLog{}
	sl := NewSlogStyleLogger(captured)
	sl.InfoContext(context.Background(), "hello", "k", "v")

	require.Len(t, captured.calls, 1)
	_, hasTraceID := captured.calls[0].fields["trace_id"]
	assert.False(t, hasTraceID, "no trace_id when extractor is nil")
}

// ── SlogStyleLogger ──────────────────────────────────────────────────────────

func TestSlogStyleLogger_AllLevelMethods_NoPanic(t *testing.T) {
	sl := NewSlogStyleLogger(NopLogger{})
	assert.NotPanics(t, func() { sl.Debug("d") })
	assert.NotPanics(t, func() { sl.Info("i") })
	assert.NotPanics(t, func() { sl.Warn("w") })
	assert.NotPanics(t, func() { sl.Error("e") })
}

func TestSlogStyleLogger_ContextMethods_NoPanic(t *testing.T) {
	sl := NewSlogStyleLogger(NopLogger{})
	ctx := context.Background()
	assert.NotPanics(t, func() { sl.DebugContext(ctx, "d") })
	assert.NotPanics(t, func() { sl.InfoContext(ctx, "i") })
	assert.NotPanics(t, func() { sl.WarnContext(ctx, "w") })
	assert.NotPanics(t, func() { sl.ErrorContext(ctx, "e") })
}

func TestSlogStyleLogger_NilLog_NoPanic(t *testing.T) {
	sl := NewSlogStyleLogger(nil)
	assert.NotPanics(t, func() { sl.Info("msg", "k", "v") })
	assert.NotPanics(t, func() { sl.InfoContext(context.Background(), "msg", "k", "v") })
}

func TestSlogStyleLogger_KeyValuePairs_MappedToFields(t *testing.T) {
	captured := &captureLog{}
	sl := NewSlogStyleLogger(captured)
	sl.Warn("warning", "code", 42, "flag", true)
	require.Len(t, captured.calls, 1)
	assert.Equal(t, 42, captured.calls[0].fields["code"])
	assert.Equal(t, true, captured.calls[0].fields["flag"])
}

func TestSlogStyleLogger_AllLevels_DispatchCorrectly(t *testing.T) {
	captured := &captureLog{}
	sl := NewSlogStyleLogger(captured)
	sl.Debug("d")
	sl.Info("i")
	sl.Warn("w")
	sl.Error("e")
	require.Len(t, captured.calls, 4)
	assert.Equal(t, "debug", captured.calls[0].level)
	assert.Equal(t, "info", captured.calls[1].level)
	assert.Equal(t, "warn", captured.calls[2].level)
	assert.Equal(t, "error", captured.calls[3].level)
}

// ── Fields / kvToFields ──────────────────────────────────────────────────────

func TestFields_KeyValuePairs_ReturnedAsMap(t *testing.T) {
	m := Fields("a", 1, "b", "two")
	assert.Equal(t, 1, m["a"])
	assert.Equal(t, "two", m["b"])
}

func TestFields_OddArgs_LastKeyIgnored(t *testing.T) {
	m := Fields("a", 1, "orphan")
	assert.Equal(t, 1, m["a"])
	_, has := m["orphan"]
	assert.False(t, has, "odd trailing key must be silently dropped")
}

func TestFields_NonStringKey_Dropped(t *testing.T) {
	m := Fields(42, "val")
	assert.Empty(t, m, "non-string key must be silently dropped")
}

// ── captureLog helper ─────────────────────────────────────────────────────────

type captureLog struct{ calls []captureCall }

type captureCall struct {
	level  string
	msg    string
	fields map[string]any
}

func (c *captureLog) Debug(msg string, fields map[string]any) {
	c.calls = append(c.calls, captureCall{"debug", msg, fields})
}
func (c *captureLog) Info(msg string, fields map[string]any) {
	c.calls = append(c.calls, captureCall{"info", msg, fields})
}
func (c *captureLog) Warn(msg string, fields map[string]any) {
	c.calls = append(c.calls, captureCall{"warn", msg, fields})
}
func (c *captureLog) Error(msg string, fields map[string]any) {
	c.calls = append(c.calls, captureCall{"error", msg, fields})
}
