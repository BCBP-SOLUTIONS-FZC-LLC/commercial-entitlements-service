package unit_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── NopLogger ──────────────────────────────────────────────────────────────

func TestNopLogger_AllMethods_NoOp(t *testing.T) {
	var l port.NopLogger
	fields := map[string]any{"key": "value"}
	// Should not panic
	l.Debug("debug msg", fields)
	l.Info("info msg", fields)
	l.Warn("warn msg", fields)
	l.Error("error msg", fields)
}

// ── SlogStyleLogger ────────────────────────────────────────────────────────

type recordingLogger struct {
	messages []string
	level    []string
}

func (r *recordingLogger) Debug(msg string, _ map[string]any) {
	r.messages = append(r.messages, msg)
	r.level = append(r.level, "debug")
}
func (r *recordingLogger) Info(msg string, _ map[string]any) {
	r.messages = append(r.messages, msg)
	r.level = append(r.level, "info")
}
func (r *recordingLogger) Warn(msg string, _ map[string]any) {
	r.messages = append(r.messages, msg)
	r.level = append(r.level, "warn")
}
func (r *recordingLogger) Error(msg string, _ map[string]any) {
	r.messages = append(r.messages, msg)
	r.level = append(r.level, "error")
}

func TestSlogStyleLogger_Debug(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.Debug("hello", "k", "v")
	assert.Equal(t, []string{"hello"}, rec.messages)
	assert.Equal(t, []string{"debug"}, rec.level)
}

func TestSlogStyleLogger_Info(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.Info("info msg", "k", "v")
	assert.Equal(t, []string{"info msg"}, rec.messages)
	assert.Equal(t, []string{"info"}, rec.level)
}

func TestSlogStyleLogger_Warn(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.Warn("warn msg")
	assert.Equal(t, []string{"warn msg"}, rec.messages)
}

func TestSlogStyleLogger_Error(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.Error("err msg", "code", 42)
	assert.Equal(t, []string{"err msg"}, rec.messages)
	assert.Equal(t, []string{"error"}, rec.level)
}

func TestSlogStyleLogger_DebugContext(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.DebugContext(context.Background(), "ctx debug")
	assert.Equal(t, []string{"ctx debug"}, rec.messages)
	assert.Equal(t, []string{"debug"}, rec.level)
}

func TestSlogStyleLogger_InfoContext(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.InfoContext(context.Background(), "ctx info", "tenant", "abc")
	assert.Equal(t, []string{"ctx info"}, rec.messages)
}

func TestSlogStyleLogger_WarnContext(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.WarnContext(context.Background(), "ctx warn")
	assert.Equal(t, []string{"ctx warn"}, rec.messages)
}

func TestSlogStyleLogger_ErrorContext(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)
	sl.ErrorContext(context.Background(), "ctx error", "err", "timeout")
	assert.Equal(t, []string{"ctx error"}, rec.messages)
	assert.Equal(t, []string{"error"}, rec.level)
}

func TestSlogStyleLogger_NilLogger_NoOp(t *testing.T) {
	sl := port.NewSlogStyleLogger(nil)
	// Should not panic when underlying logger is nil
	sl.Debug("no-op")
	sl.Info("no-op")
	sl.Warn("no-op")
	sl.Error("no-op")
	sl.DebugContext(context.Background(), "no-op")
	sl.InfoContext(context.Background(), "no-op")
	sl.WarnContext(context.Background(), "no-op")
	sl.ErrorContext(context.Background(), "no-op")
}

func TestSlogStyleLogger_WithTraceExtractor(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)

	port.SetTraceExtractor(func(_ context.Context) string { return "trace-abc" })
	t.Cleanup(func() { port.SetTraceExtractor(nil) })

	sl.InfoContext(context.Background(), "traced msg", "k", "v")
	assert.Equal(t, []string{"traced msg"}, rec.messages)
}

func TestSlogStyleLogger_WithTraceExtractor_EmptyID(t *testing.T) {
	rec := &recordingLogger{}
	sl := port.NewSlogStyleLogger(rec)

	port.SetTraceExtractor(func(_ context.Context) string { return "" })
	t.Cleanup(func() { port.SetTraceExtractor(nil) })

	sl.InfoContext(context.Background(), "untraced msg")
	assert.Equal(t, []string{"untraced msg"}, rec.messages)
}

// ── Fields ─────────────────────────────────────────────────────────────────

func TestFields_PairsKVArgs(t *testing.T) {
	f := port.Fields("key1", "val1", "key2", 42)
	assert.Equal(t, "val1", f["key1"])
	assert.Equal(t, 42, f["key2"])
}

func TestFields_OddArgs_LastDropped(t *testing.T) {
	f := port.Fields("key1", "val1", "orphaned")
	assert.Equal(t, "val1", f["key1"])
	_, ok := f["orphaned"]
	assert.False(t, ok)
}

func TestFields_Empty(t *testing.T) {
	f := port.Fields()
	assert.Empty(t, f)
}

func TestFields_NonStringKey_Dropped(t *testing.T) {
	f := port.Fields(42, "value")
	assert.Empty(t, f)
}
