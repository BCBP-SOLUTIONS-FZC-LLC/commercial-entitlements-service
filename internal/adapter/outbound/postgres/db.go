// Package postgres implements the outbound repository ports backed by
// PostgreSQL through platform-pgcommon. TxRunner is the seam that binds
// transaction-local context, tx-bound outbox publisher and audit recorder
// onto every write.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// DSNFromEnv builds a PostgreSQL connection URL for the application pool.
// DATABASE_URL takes precedence; falls back to assembling from PG_* vars.
func DSNFromEnv() string {
	cfg, _ := pgcommon.ConfigFromEnv()
	return cfg.DSN
}

// MigrationDSNFromEnv returns the DSN for schema migrations.
// MIGRATION_DATABASE_URL when set, else the app DSN.
// Migrations MUST bypass PgBouncer because the migration runner uses
// pg_advisory_lock (session-scoped, breaks under transaction pooling — CONFIG-2).
func MigrationDSNFromEnv() string {
	return pgcommon.MigrationDSNFromEnv()
}

// SystemDSNFromEnv returns the BYPASSRLS connection string used by the
// reconciler and audit runner. Falls back to DSNFromEnv() for local dev.
func SystemDSNFromEnv() string {
	if dsn := os.Getenv("SYSTEM_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return DSNFromEnv()
}

// SystemPoolConfig returns pgcommon.Config for the BYPASSRLS system pool.
// No GUCProvider (cross-tenant), PGBouncerMode forced true so reconciler
// jobs do not break transaction pooling.
func SystemPoolConfig(dsn string, log port.Logger) pgcommon.Config {
	cfg, _ := pgcommon.ConfigFromEnv()
	cfg.DSN = dsn
	cfg.PoolName = "system"
	cfg.GUCProvider = nil
	cfg.PGBouncerMode = true
	if log != nil {
		cfg.Logger = NewLoggerAdapter(log)
	}
	return cfg
}

// TxRunner wraps pgcommon.Pool to implement port.TxRunner. When a
// port.EventPublisher is provided it is injected into the tx context so
// services can enqueue events atomically via
// port.EventPublisherFromContext(txCtx).
type TxRunner struct {
	pool   *pgcommon.Pool
	events port.EventPublisher
	audit  port.AuditRecorder
}

// NewTxRunner constructs a TxRunner. Pass nil for events during bootstrap
// paths (migration-only reconciler runs) where no outbox writes occur.
func NewTxRunner(pool *pgcommon.Pool, events port.EventPublisher) *TxRunner {
	return &TxRunner{pool: pool, events: events}
}

// WithAuditRecorder makes RunInTx inject rec into the tx context so services
// enqueue Audit Log entries atomically with the business write via
// port.AuditRecorderFromContext(txCtx).
func (r *TxRunner) WithAuditRecorder(rec port.AuditRecorder) *TxRunner {
	r.audit = rec
	return r
}

// writeRetryOpts: deadlock (40P01) and serialization failure (40001) retry
// with exponential backoff + jitter.
var writeRetryOpts = pgcommon.RetryOptions{
	MaxAttempts:    3,
	InitialWait:    10 * time.Millisecond,
	MaxWait:        500 * time.Millisecond,
	Multiplier:     2.0,
	JitterFraction: 0.25,
}

// RunInTx opens a transaction on pool, stores it in ctx via port.WithTx,
// injects the EventPublisher and AuditRecorder, then calls fn. Commits on
// success; rolls back on error. Contended writes retry on deadlock /
// serialization failure via pgcommon.RunInTxWithRetryOpts.
//
// Join mode: when a transaction is already present in ctx (placed there by
// platform-events inbox.Store.Process for the processed_events claim), RunInTx
// reuses it instead of opening a new one. This keeps the dedup claim, the
// snapshot upsert, and the outbox.Enqueue atomically in one transaction
// (IDEMP-1..4, EVT-10). The retry logic is not applied in join mode — the
// outer inbox transaction handles retries via SQS message redelivery.
func (r *TxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := port.TxFromContext(ctx); ok {
		// Already inside a transaction (inbox.Store.Process or nested call).
		// Inject publishers fresh without opening a second connection.
		txCtx := ctx
		if r.events != nil {
			txCtx = port.WithEventPublisher(txCtx, r.events)
		}
		if r.audit != nil {
			txCtx = port.WithAuditRecorder(txCtx, r.audit)
		}
		return wrapConnErr(fn(txCtx))
	}
	return wrapConnErr(pgcommon.RunInTxWithRetryOpts(ctx, r.pool, pgcommon.TxOptions{}, writeRetryOpts, func(ctx context.Context, tx pgcommon.Tx) error {
		txCtx := port.WithTx(ctx, tx)
		if r.events != nil {
			txCtx = port.WithEventPublisher(txCtx, r.events)
		}
		if r.audit != nil {
			txCtx = port.WithAuditRecorder(txCtx, r.audit)
		}
		return fn(txCtx)
	}))
}

// withPool runs fn inside a transaction, joining an existing one if present
// in ctx. Used by repository helpers so reads outside a service tx still use
// the pool via pgcommon.RunInTx.
func withPool(ctx context.Context, pool *pgcommon.Pool, fn func(pgcommon.Tx) error) error {
	if tx, ok := port.TxFromContext(ctx); ok {
		return wrapConnErr(fn(tx))
	}
	return wrapConnErr(pgcommon.RunInTx(ctx, pool, pgcommon.TxOptions{}, func(ctx context.Context, tx pgcommon.Tx) error {
		return fn(tx)
	}))
}

// wrapConnErr converts non-protocol database errors into domain error types.
// SQL-protocol errors (*pgconn.PgError) for unique violations, FK, check pass
// through so the service layer can distinguish them.
func wrapConnErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.DomainError
	if errors.As(err, &de) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &domain.DomainError{
			Code:    domain.ErrRequestTimeout.Error(),
			Message: "request deadline exceeded during a database operation",
			Cause:   fmt.Errorf("%w: %w", domain.ErrRequestTimeout, err),
		}
	}
	if pgcommon.IsDeadlock(err) || pgcommon.IsSerializationFailure(err) {
		return &domain.DomainError{
			Code:    domain.ErrConcurrentUpdate.Error(),
			Message: "concurrent update conflict — retry the request",
			Cause:   fmt.Errorf("%w: %w", domain.ErrConcurrentUpdate, err),
		}
	}
	if pgcommon.IsQueryCanceled(err) || pgcommon.IsLockNotAvailable(err) {
		return &domain.DomainError{
			Code:    domain.ErrRequestTimeout.Error(),
			Message: "database statement or lock timeout",
			Cause:   fmt.Errorf("%w: %w", domain.ErrRequestTimeout, err),
		}
	}
	if isDBUnavailable(err) {
		return &domain.DomainError{
			Code:    domain.ErrDBUnavailable.Error(),
			Message: "database unavailable",
			Cause:   fmt.Errorf("%w: %w", domain.ErrDBUnavailable, err),
		}
	}
	return err
}

// isDBUnavailable reports whether err is a database availability failure.
func isDBUnavailable(err error) bool {
	var connectErr *pgconn.ConnectError
	return pgcommon.IsConnectionException(err) || pgcommon.IsInsufficientResources(err) ||
		pgcommon.IsOperatorIntervention(err) || pgcommon.IsSystemError(err) || pgcommon.IsPoolClosed(err) ||
		errors.As(err, &connectErr)
}

// itoa converts int to string for use in raw SQL LIMIT clauses.
func itoa(n int) string {
	if n <= 0 {
		return "100"
	}
	return strconv.Itoa(n)
}

// Ensure TxRunner implements port.TxRunner at compile time.
var _ port.TxRunner = (*TxRunner)(nil)
