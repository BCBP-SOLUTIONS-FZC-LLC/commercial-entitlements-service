package postgres

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// LoggerAdapter implements platform-pgcommon's pkg/domain.Logger (Debug/Info/
// Warn/Error(msg, ...domain.Field)) on top of port.Logger. pgcommon v1.2.0
// retyped Config.Logger against the public domain.Logger interface so this
// service's gincommon-backed Zap sink handles pgcommon slow-query and pool log
// output instead of going nowhere.
type LoggerAdapter struct {
	log port.Logger
}

var _ domain.Logger = LoggerAdapter{}

// NewLoggerAdapter wraps log as a pgcommon domain.Logger.
func NewLoggerAdapter(log port.Logger) LoggerAdapter {
	return LoggerAdapter{log: log}
}

func (a LoggerAdapter) Debug(msg string, fields ...domain.Field) { a.log.Debug(msg, fieldMap(fields)) }
func (a LoggerAdapter) Info(msg string, fields ...domain.Field)  { a.log.Info(msg, fieldMap(fields)) }
func (a LoggerAdapter) Warn(msg string, fields ...domain.Field)  { a.log.Warn(msg, fieldMap(fields)) }
func (a LoggerAdapter) Error(msg string, fields ...domain.Field) { a.log.Error(msg, fieldMap(fields)) }

func fieldMap(fields []domain.Field) map[string]any {
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		m[f.Key] = f.Value
	}
	return m
}
