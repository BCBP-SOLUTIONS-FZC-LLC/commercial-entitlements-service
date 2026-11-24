// Package auditrecorder implements port.AuditRecorder on platform-audit's
// transactional outbox: entries are written into pending_audit_entries on the
// caller's transaction (port.TxFromContext) and delivered to the audit log
// service by the auditoutbox.Runner running on the BYPASSRLS sysPool.
// An entry exists if and only if the audited change committed (PA-INV-3).
package auditrecorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-audit/pkg/audit"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-audit/pkg/auditoutbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ErrNoTransaction is returned when Record is called outside TxRunner.RunInTx.
var ErrNoTransaction = errors.New("auditrecorder: no transaction in context (call inside TxRunner.RunInTx)")

// AuditRecorder implements port.AuditRecorder.
type AuditRecorder struct {
	enq *auditoutbox.Enqueuer
}

// New wraps enq.
func New(enq *auditoutbox.Enqueuer) *AuditRecorder {
	return &AuditRecorder{enq: enq}
}

var _ port.AuditRecorder = (*AuditRecorder)(nil)

// Record enqueues a single audit entry inside the caller's transaction.
func (r *AuditRecorder) Record(ctx context.Context, e port.AuditEntry) error {
	tx, ok := port.TxFromContext(ctx)
	if !ok {
		return ErrNoTransaction
	}
	entry, err := toEntry(e)
	if err != nil {
		return err
	}
	return r.enq.Enqueue(ctx, tx, entry, e.IdempotencyKey)
}

// RecordBatch enqueues multiple audit entries in a single transaction round-trip.
func (r *AuditRecorder) RecordBatch(ctx context.Context, entries []port.AuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, ok := port.TxFromContext(ctx)
	if !ok {
		return ErrNoTransaction
	}
	items := make([]auditoutbox.Item, 0, len(entries))
	for _, e := range entries {
		entry, err := toEntry(e)
		if err != nil {
			return err
		}
		items = append(items, auditoutbox.Item{Entry: entry, Key: e.IdempotencyKey})
	}
	return r.enq.EnqueueBatch(ctx, tx, items)
}

// toEntry maps the port entry onto audit.Entry.
func toEntry(e port.AuditEntry) (audit.Entry, error) {
	var tenantID interface{ String() string }
	if e.TenantID != nil {
		tenantID = e.TenantID
	}
	out := audit.Entry{
		EntryType:       e.EntryType,
		Action:          e.Action,
		Actor:           audit.Actor{Type: audit.ActorType(e.Actor.Type), ID: e.Actor.ID},
		OccurredAt:      e.OccurredAt,
		SourceEventType: e.SourceEventType,
		IPAddress:       e.IPAddress,
		UserAgent:       e.UserAgent,
	}
	// TenantID is uuid.UUID (not pointer) in audit.Entry; use zero if nil.
	if tenantID != nil {
		out.TenantID = *e.TenantID
	}
	if e.Target != nil {
		out.Target = &audit.Target{Type: e.Target.Type, ID: e.Target.ID}
	}
	if len(e.Metadata) > 0 {
		md, err := audit.Metadata(e.Metadata)
		if err != nil {
			return audit.Entry{}, fmt.Errorf("auditrecorder: %s metadata: %w", e.EntryType, err)
		}
		out.Metadata = md
	}
	return out, nil
}

// Config holds audit delivery configuration loaded from environment.
type Config struct {
	BaseURL   string
	TimeoutMS int
}

// LoadConfig loads platform-audit configuration.
func LoadConfig() (Config, error) {
	return Config{
		BaseURL:   os.Getenv("AUDIT_LOG_BASE_URL"),
		TimeoutMS: envInt("AUDIT_LOG_TIMEOUT_MS", 5000),
	}, nil
}

// EnqueueConfig returns auditoutbox.EnqueueConfig.
func (c Config) EnqueueConfig(log port.Logger) auditoutbox.EnqueueConfig {
	return auditoutbox.EnqueueConfig{
		SourceService: "commercial-entitlements",
		Logger:        auditLogger{log},
	}
}

// RunnerConfig returns auditoutbox.Config.
// pool is the BYPASSRLS *pgcommon.Pool so the runner can read all tenants' entries.
func (c Config) RunnerConfig(pool *pgcommon.Pool, client *audit.Client, enq *auditoutbox.Enqueuer, log port.Logger) auditoutbox.Config {
	return auditoutbox.Config{
		Pool:        pool,
		Client:      client,
		Enqueuer:    enq,
		SystemActor: audit.CommercialSystemActor(),
		Logger:      auditLogger{log},
	}
}

// NewClient returns an audit.Client, or nil when base URL is unset.
func (c Config) NewClient(log port.Logger) (*audit.Client, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		log.Warn("AUDIT_LOG_BASE_URL unset — audit delivery disabled", nil)
		return nil, nil
	}
	return audit.NewClient(audit.ClientConfig{
		BaseURL: c.BaseURL,
		Timeout: time.Duration(c.TimeoutMS) * time.Millisecond,
	})
}

type auditLogger struct{ l port.Logger }

func (a auditLogger) Info(msg string, fields map[string]any) {
	a.l.Info(msg, fields)
}
func (a auditLogger) Warn(msg string, fields map[string]any) {
	a.l.Warn(msg, fields)
}
func (a auditLogger) Error(msg string, fields map[string]any) {
	a.l.Error(msg, fields)
}
func (a auditLogger) Debug(msg string, fields map[string]any) {
	a.l.Debug(msg, fields)
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
