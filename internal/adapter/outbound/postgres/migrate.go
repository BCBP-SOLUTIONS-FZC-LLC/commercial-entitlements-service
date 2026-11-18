package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-audit/pkg/auditoutbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/outbox"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	pgmigrate "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/migrate"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies all pending domain migrations against dsn.
// Uses the direct Postgres DSN (bypassing PgBouncer, CONFIG-2) because
// the runner acquires a pg_advisory_lock which is session-scoped.
func RunMigrations(ctx context.Context, dsn string, log ...port.Logger) error {
	sub, _ := fs.Sub(migrationsFS, "migrations")
	runner := &pgmigrate.Runner{FS: sub, DSN: dsn}
	if len(log) > 0 && log[0] != nil {
		runner.Logger = NewLoggerAdapter(log[0])
	}
	return runner.Up(ctx)
}

// MigrateSchema brings the database to this build's schema:
//  1. platform-events' outbox schema (outbox_events)
//  2. platform-audit's audit outbox schema (pending_audit_entries)
//  3. domain migrations from migrations/000001_initial_schema.up.sql
//
// Run once per release via the migrate Job (cmd/migrate, Helm pre-install/
// pre-upgrade hook). With RUN_MIGRATIONS unset or "true", cmd/server also
// runs it at startup for local dev and tests.
func MigrateSchema(ctx context.Context, dsn string, log port.Logger) error {
	var logger pgdomain.Logger
	if log != nil {
		logger = NewLoggerAdapter(log)
	}
	if err := outbox.ApplySchema(ctx, &pgmigrate.Runner{DSN: dsn, Logger: logger}); err != nil {
		return explainMigrationError("outbox schema", err)
	}
	if err := auditoutbox.ApplySchema(ctx, &pgmigrate.Runner{DSN: dsn, Logger: logger}); err != nil {
		return explainMigrationError("audit outbox schema", err)
	}
	if err := RunMigrations(ctx, dsn, log); err != nil {
		return explainMigrationError("domain migrations", err)
	}
	return nil
}

func explainMigrationError(stage string, err error) error {
	switch {
	case errors.Is(err, pgmigrate.ErrMigrationDirty):
		return fmt.Errorf("%s: a previous migration failed and left the schema dirty — repair it by hand: %w", stage, err)
	case errors.Is(err, pgmigrate.ErrVersionNotInSource):
		return fmt.Errorf("%s: the database schema is newer than this image — deploy a build containing the migration: %w", stage, err)
	default:
		return fmt.Errorf("%s: %w", stage, err)
	}
}
