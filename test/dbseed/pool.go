// Package dbseed wraps pgcommon.Pool with helpers for test seed/assert SQL.
// Construction uses pgcommon.NewPool without a GUCProvider so tests can seed
// data as the connected superuser and read it back via the app pool, bypassing
// RLS. All database access goes through platform-pgcommon per the platform
// observability standard.
package dbseed

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

// Pool wraps pgcommon.Pool for test seed/assert operations.
type Pool struct {
	inner *pgcommon.Pool
	dsn   string
}

// New opens a pgcommon.Pool against dsn (typically a testcontainer superuser URL).
// No GUCProvider is wired so queries run without the app.tenant_id GUC — this
// bypasses RLS intentionally for seeding cross-tenant test fixtures.
func New(ctx context.Context, dsn string) (*Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:         dsn,
		GUCProvider: nil, // no RLS — superuser for test seeding
	})
	if err != nil {
		return nil, fmt.Errorf("dbseed.New: %w", err)
	}
	return &Pool{inner: pool, dsn: dsn}, nil
}

// Exec executes a SQL statement inside an auto-committed transaction (bypasses
// RLS). The CommandTag is intentionally discarded; callers only need to know
// whether the statement succeeded.
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) error {
	return p.inner.WithTx(ctx, pgcommon.TxOptions{}, func(ctx context.Context, tx pgcommon.Tx) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	})
}

// RunQueryRow executes a single-row query and passes the Row to scan inside
// the same transaction. scan must call row.Scan before returning.
//
// Usage:
//
//	err := pool.RunQueryRow(ctx, func(row pgcommon.Row) error {
//	    return row.Scan(&dest)
//	}, `SELECT … WHERE id=$1`, id)
func (p *Pool) RunQueryRow(ctx context.Context, scan func(pgcommon.Row) error, sql string, args ...any) error {
	return p.inner.WithTx(ctx, pgcommon.TxOptions{}, func(ctx context.Context, tx pgcommon.Tx) error {
		return scan(tx.QueryRow(ctx, sql, args...))
	})
}

// WithTx runs fn inside a single auto-committed transaction on the seed pool.
// Useful when a test needs raw pgcommon.Tx access, e.g. to call
// outbox.Enqueue (platform-events) or platform-pgcommon helpers.
func (p *Pool) WithTx(ctx context.Context, fn func(ctx context.Context, tx pgcommon.Tx) error) error {
	return p.inner.WithTx(ctx, pgcommon.TxOptions{}, fn)
}

// Close releases the pool.
func (p *Pool) Close() {
	p.inner.Close()
}

// DSN returns the connection string this pool was opened against.
func (p *Pool) DSN() string { return p.dsn }
