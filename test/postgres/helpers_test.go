//go:build integration

// Package postgres_test contains Phase 5 Postgres integration tests.
// Each test spins its own testcontainers Postgres (create+migrate, ~1–2 s)
// and runs the full RLS-aware app pool. t.Parallel() is called in every
// test so the suite runs up to TEST_POSTGRES_PARALLEL containers in parallel.
package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/test/dbseed"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

const (
	postgresImage    = "postgres:16-alpine"
	appRole          = "entitlements_app"
	appRolePassword  = "apppassword-testonly"
	migratorPassword = "migratorpassword-testonly"
	dbName           = "entitlements_test"
)

// setupTestDB starts a Postgres container, runs migrations, creates the app
// role, and returns three pools plus the default tenant ID. The container is
// terminated when t ends.
//
//   - appPool  — RLS-enforced app role (entitlements_app NOBYPASSRLS); tenant
//     is injected via GUC from ctx on every query.
//   - seedPool — superuser connection (postgres role); used to seed rows that
//     bypass RLS or for SQL assertions after the fact.
//   - sysPool  — BYPASSRLS system pool (superuser, no GUCProvider); used for
//     cross-tenant queries such as ListTenantIDsByPlan and reconciler reads.
//     Mirrors IAM org-membership's three-pool pattern.
func setupTestDB(t *testing.T) (*pgcommon.Pool, *dbseed.Pool, *pgcommon.Pool, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(migratorPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	superDSN, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create the application role BEFORE running migrations.
	// Migrations (000002, 000003) contain GRANT statements that reference
	// entitlements_app — the role must already exist when they run.
	// This matches production: the DBA pre-creates the role and the migration
	// only grants permissions to it. (Pattern from iam-org-membership §rls_test.go)
	seedPool, err := dbseed.New(ctx, superDSN)
	require.NoError(t, err)
	t.Cleanup(seedPool.Close)

	err = seedPool.Exec(ctx, fmt.Sprintf(
		`CREATE ROLE %s LOGIN PASSWORD '%s' NOBYPASSRLS`,
		appRole, appRolePassword,
	))
	require.NoError(t, err)

	// Run migrations (now that the role exists, GRANTs inside migrations succeed).
	require.NoError(t, pgadapter.MigrateSchema(ctx, superDSN, nil))

	// Belt-and-suspenders: also grant CONNECT + USAGE for containers that
	// do not apply the GRANTs inside migrations (e.g. fresh schema).
	err = seedPool.Exec(ctx, fmt.Sprintf(`
		GRANT CONNECT ON DATABASE %s TO %s;
		GRANT USAGE ON SCHEMA public TO %s;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s;
		GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %s;
	`, dbName, appRole, appRole, appRole, appRole))
	require.NoError(t, err)

	// Build app DSN using the app role.
	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	port, err := ctr.MappedPort(ctx, "5432")
	require.NoError(t, err)
	appDSN := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		appRole, appRolePassword, host, port.Port(), dbName,
	)

	tenantID := uuid.New()
	appPool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:           appDSN,
		PGBouncerMode: false,
		GUCProvider: func(ctx context.Context) (pgdomain.GUCSet, bool) {
			return pgdomain.GUCSet{TenantID: tenantFromCtx(ctx, tenantID).String()}, true
		},
	})
	require.NoError(t, err)
	t.Cleanup(appPool.Close)

	// sysPool: superuser with no GUCProvider — bypasses RLS for cross-tenant
	// queries (ListTenantIDsByPlan, reconciler reads, error-path seeding).
	sysPool, err := pgcommon.NewPool(ctx, pgadapter.SystemPoolConfig(superDSN, nil))
	require.NoError(t, err)
	t.Cleanup(sysPool.Close)

	return appPool, seedPool, sysPool, tenantID
}

// tenantFromCtx returns the tenant ID stored in ctx, falling back to def.
func tenantFromCtx(ctx context.Context, def uuid.UUID) uuid.UUID {
	if id, ok := ctx.Value(ctxKeyTenant{}).(uuid.UUID); ok {
		return id
	}
	return def
}

type ctxKeyTenant struct{}

// withTenant stores tenantID in ctx so the GUC provider picks it up.
func withTenant(ctx context.Context, tenantID uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyTenant{}, tenantID)
}

// sampleSnapshot returns a minimal valid EntitlementSnapshot for testing.
func sampleSnapshot(tenantID uuid.UUID, version int64) *domain.EntitlementSnapshot {
	return &domain.EntitlementSnapshot{
		TenantID:            tenantID,
		Version:             version,
		SourceRecordVersion: version,
		Plan:                "starter",
		State:               domain.EntitlementStateActive,
		FeatureFlags:        []string{},
		ModelTier:           "standard",
		Limits: map[domain.LimitKey]domain.LimitValue{
			domain.LimitKeyLLMTokenQuotaMonthly: {Value: func() *int64 { v := int64(100_000); return &v }()},
		},
		Cause:      domain.CausePlanChange,
		ComputedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
}

// sampleAddon returns a minimal valid QuotaAddon for testing.
func sampleAddon(tenantID uuid.UUID, key domain.LimitKey) *domain.QuotaAddon {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return &domain.QuotaAddon{
		ID:              uuid.New(),
		TenantID:        tenantID,
		LimitKey:        key,
		TotalTokens:     50_000,
		RemainingTokens: 50_000,
		GrantedBy:       uuid.New(),
		Reason:          "test grant",
		Status:          domain.AddonStatusActive,
		RecordVersion:   1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// sampleOverride returns a minimal valid Override for testing.
func sampleOverride(tenantID uuid.UUID, key domain.LimitKey) *domain.Override {
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := int64(200_000)
	return &domain.Override{
		TenantID:      tenantID,
		LimitKey:      key,
		Value:         &v,
		Reason:        "test override",
		SetBy:         uuid.New(),
		RecordVersion: 1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}
