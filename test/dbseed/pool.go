// Package dbseed wraps pgxpool.Pool with helpers for test seed/assert SQL.
// Construction uses the superuser DSN directly (no RLS, no GUC) so tests
// can seed data as postgres and then read it back via the app pool.
package dbseed

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool wraps pgxpool.Pool for test seed/assert operations.
type Pool struct {
	inner *pgxpool.Pool
	dsn   string
}

// New opens a pgxpool against dsn (typically the testcontainer superuser URL).
func New(ctx context.Context, dsn string) (*Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("dbseed.New: %w", err)
	}
	return &Pool{inner: pool, dsn: dsn}, nil
}

// Exec executes a SQL statement using the pool directly (bypasses RLS).
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.inner.Exec(ctx, sql, args...)
}

// QueryRow executes a query expected to return one row.
func (p *Pool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.inner.QueryRow(ctx, sql, args...)
}

// Query executes a query that returns multiple rows.
func (p *Pool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.inner.Query(ctx, sql, args...)
}

// Close releases the pool.
func (p *Pool) Close() {
	p.inner.Close()
}

// DSN returns the connection string this pool was opened against.
func (p *Pool) DSN() string { return p.dsn }
