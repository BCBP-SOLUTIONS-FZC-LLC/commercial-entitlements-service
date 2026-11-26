//go:build integration

// Phase 5 · P5-MIG — migrations apply cleanly and idempotently.
package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

// Test Case ID:      P5-MIG-001
// Module:            commercial-entitlements · Persistence
// Feature:           Schema migration idempotency
// Scenario:          Running MigrateSchema twice on the same database succeeds
// Expected Result:   Second call returns no error
// createRoleForMigration mirrors scripts/init-db.sql: creates both the
// production app role (commercial_entitlements_app) and the legacy placeholder
// role (entitlements_app) before migrations run.
//
// Two roles are needed because migration 000002 (on main, immutable per MIG-1)
// contains an unconditional GRANT to the old name "entitlements_app". Without
// this placeholder role the migration hard-fails on a fresh database.
// Migrations 000003 and 000004 revoke from entitlements_app and re-grant to
// commercial_entitlements_app.
func createRoleForMigration(t *testing.T, ctx context.Context, superDSN string) {
	t.Helper()
	raw, err := dbseedNew(ctx, superDSN)
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	err = raw.Exec(ctx, "CREATE ROLE "+appRole+" LOGIN PASSWORD '"+appRolePassword+"' NOBYPASSRLS")
	require.NoError(t, err, "create "+appRole+" role before migrations")
	err = raw.Exec(ctx, "CREATE ROLE entitlements_app NOLOGIN")
	require.NoError(t, err, "create entitlements_app placeholder before migrations")
}

func TestP5Mig001_MigrateSchemaIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(migratorPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	createRoleForMigration(t, ctx, dsn)
	require.NoError(t, pgadapter.MigrateSchema(ctx, dsn, nil), "first migration must succeed")
	require.NoError(t, pgadapter.MigrateSchema(ctx, dsn, nil), "second migration must be a no-op")
}

// Test Case ID:      P5-MIG-002
// Module:            commercial-entitlements · Persistence
// Feature:           Schema migration — expected tables exist
// Scenario:          After MigrateSchema all domain tables are present
// Expected Result:   information_schema.tables contains each expected table
func TestP5Mig002_ExpectedTablesExist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(migratorPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	createRoleForMigration(t, ctx, dsn)
	require.NoError(t, pgadapter.MigrateSchema(ctx, dsn, nil))

	seedPool, err := dbseedNew(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(seedPool.Close)

	expected := []string{
		"entitlement_snapshots",
		"quota_addons",
		"quota_addon_drawdowns",
		"quota_addon_shortfalls",
		"entitlement_overrides",
		"entitlement_override_history",
		"tenant_trial_assignments",
		"trial_entitlement_overrides",
		"outbox_events",
		"pending_audit_entries",
	}
	for _, table := range expected {
		var exists bool
		err := seedPool.RunQueryRow(ctx, func(row pgcommon.Row) error {
			return row.Scan(&exists)
		}, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name=$1)`, table)
		require.NoError(t, err, "querying existence of table %s", table)
		assert.True(t, exists, "expected table %s to exist after migration", table)
	}
}

// Test Case ID:      P5-MIG-003
// Module:            commercial-entitlements · Persistence
// Feature:           Schema migration — outbox_events table accessible
// Scenario:          Outbox schema is applied; outbox_events can be inserted into
// Expected Result:   INSERT into outbox_events succeeds
func TestP5Mig003_OutboxTableAccessible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(migratorPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	createRoleForMigration(t, ctx, dsn)
	require.NoError(t, pgadapter.MigrateSchema(ctx, dsn, nil))

	seedPool, err := dbseedNew(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(seedPool.Close)

	// Use outbox.Enqueue (platform-events) rather than raw SQL — this verifies
	// both that the outbox_events table was created by the migration AND that
	// platform-events can write to it, per the platform observability standard
	// (all event writes pass through platform-events only).
	err = seedPool.WithTx(ctx, func(ctx context.Context, tx pgcommon.Tx) error {
		return outbox.Enqueue(ctx, tx, events.NewEnvelope(
			"TestMigration", "commercial-entitlements-test", json.RawMessage(`{}`),
		))
	})
	require.NoError(t, err, "outbox.Enqueue must succeed after migration")
}

// TestMigrateSchema_WithLogger covers the log != nil branch in MigrateSchema
// and the len(log) > 0 && log[0] != nil branch in RunMigrations.
func TestMigrateSchema_WithLogger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(migratorPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	createRoleForMigration(t, ctx, dsn)

	// Pass a non-nil logger — this exercises the log != nil branch.
	require.NoError(t, pgadapter.MigrateSchema(ctx, dsn, nopMigrateLogger{}))
}

type nopMigrateLogger struct{}

func (nopMigrateLogger) Debug(_ string, _ map[string]any) {}
func (nopMigrateLogger) Info(_ string, _ map[string]any)  {}
func (nopMigrateLogger) Warn(_ string, _ map[string]any)  {}
func (nopMigrateLogger) Error(_ string, _ map[string]any) {}
