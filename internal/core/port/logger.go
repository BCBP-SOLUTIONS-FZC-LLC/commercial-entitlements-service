// Package port defines the inbound and outbound interfaces (ports) for the
// Commercial Entitlements service.
package port

import (
	"context"
)

// Logger is the structured logging port used throughout this service. It
// matches platform-gincommon's own port.Logger shape exactly
// (Debug/Info/Warn/Error(msg, fields)), so the single Zap-backed logger
// built via platform-gincommon's logger.NewLogger (cmd/server/main.go,
// cmd/reconciler/main.go) satisfies it directly — no adapter needed.
type Logger interface {
	Debug(msg string, fields map[string]any)
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
	Error(msg string, fields map[string]any)
}

// traceExtractorFn is installed once at startup by cmd/server and
// cmd/reconciler via SetTraceExtractor. nil is safe.
var traceExtractorFn func(context.Context) string

// SetTraceExtractor installs the OTel trace ID extractor. Called after
// gincommon.InitTracingWithConfig.
func SetTraceExtractor(fn func(context.Context) string) {
	traceExtractorFn = fn
}

// logLevel mirrors the slog integer convention (slog.LevelDebug=-4,
// slog.LevelInfo=0, slog.LevelWarn=4, slog.LevelError=8) so SlogStyleLogger
// can dispatch by level without importing log/slog, keeping the core port
// package free of infrastructure dependencies.
type logLevel int8

const (
	levelDebug logLevel = -4
	levelInfo  logLevel = 0
	levelWarn  logLevel = 4
	levelError logLevel = 8
)

// SlogStyleLogger adapts a Logger to slog-style call conventions
// (msg, "key", val, ...) and *Context variants that also attach trace_id.
// The zero value is safe (no-op).
type SlogStyleLogger struct {
	log Logger
}

// NewSlogStyleLogger wraps log for slog-style call sites.
func NewSlogStyleLogger(log Logger) SlogStyleLogger {
	return SlogStyleLogger{log: log}
}

func (s SlogStyleLogger) Debug(msg string, args ...any) {
	s.log4(context.Background(), levelDebug, msg, args, false)
}
func (s SlogStyleLogger) Info(msg string, args ...any) {
	s.log4(context.Background(), levelInfo, msg, args, false)
}
func (s SlogStyleLogger) Warn(msg string, args ...any) {
	s.log4(context.Background(), levelWarn, msg, args, false)
}
func (s SlogStyleLogger) Error(msg string, args ...any) {
	s.log4(context.Background(), levelError, msg, args, false)
}

func (s SlogStyleLogger) DebugContext(ctx context.Context, msg string, args ...any) {
	s.log4(ctx, levelDebug, msg, args, true)
}
func (s SlogStyleLogger) InfoContext(ctx context.Context, msg string, args ...any) {
	s.log4(ctx, levelInfo, msg, args, true)
}
func (s SlogStyleLogger) WarnContext(ctx context.Context, msg string, args ...any) {
	s.log4(ctx, levelWarn, msg, args, true)
}
func (s SlogStyleLogger) ErrorContext(ctx context.Context, msg string, args ...any) {
	s.log4(ctx, levelError, msg, args, true)
}

func (s SlogStyleLogger) log4(ctx context.Context, level logLevel, msg string, args []any, withCtx bool) {
	if withCtx && traceExtractorFn != nil {
		if id := traceExtractorFn(ctx); id != "" {
			args = append(append([]any{}, args...), "trace_id", id)
		}
	}
	if s.log == nil {
		return
	}
	fields := kvToFields(args)
	switch level {
	case levelDebug:
		s.log.Debug(msg, fields)
	case levelInfo:
		s.log.Info(msg, fields)
	case levelWarn:
		s.log.Warn(msg, fields)
	case levelError:
		s.log.Error(msg, fields)
	}
}

// Fields pairs slog-style key/value args into the map shape Logger expects.
func Fields(kv ...any) map[string]any {
	return kvToFields(kv)
}

// kvToFields pairs alternating string-key/value slog-style args into a map.
func kvToFields(args []any) map[string]any {
	fields := make(map[string]any, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		if key, ok := args[i].(string); ok {
			fields[key] = args[i+1]
		}
	}
	return fields
}

// NopLogger discards all log output. Useful in tests.
type NopLogger struct{}

func (NopLogger) Info(_ string, _ map[string]any)  {}
func (NopLogger) Warn(_ string, _ map[string]any)  {}
func (NopLogger) Error(_ string, _ map[string]any) {}
func (NopLogger) Debug(_ string, _ map[string]any) {}
