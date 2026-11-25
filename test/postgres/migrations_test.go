//go:build integration

// Phase 5 · P5-MIG — migrations apply cleanly and idempotently.
package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
)

// Test Case ID:      P5-MIG-001
// Module:            commercial-entitlements · Persistence
// Feature:           Schema migration idempotency
// Scenario:          Running MigrateSchema twice on the same database succeeds
// Expected Result:   Second call returns no error
func TestP5Mig001_MigrateSchemaIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(migratorPassword),
		tcpostgres.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

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
		tcpostgres.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
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
		err := seedPool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name=$1)`,
			table,
		).Scan(&exists)
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
		tcpostgres.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, pgadapter.MigrateSchema(ctx, dsn, nil))

	seedPool, err := dbseedNew(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(seedPool.Close)

	_, err = seedPool.Exec(ctx, `
		INSERT INTO outbox_events (id, event_type, payload, created_at)
		VALUES (gen_random_uuid(), 'TestEvent', '{}', now())`)
	require.NoError(t, err, "outbox_events INSERT must succeed after migration")
}
