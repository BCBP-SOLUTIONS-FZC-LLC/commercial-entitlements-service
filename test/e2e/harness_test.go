//go:build e2e

// Package e2e_test spins up the full commercial-entitlements HTTP stack
// against a real Postgres testcontainer and exposes it via httptest.Server.
// Tests hit the running server over net/http — the same path as production.
//
// Design:
//   - Postgres 16 testcontainer per test (own schema migration)
//   - Full real Postgres-backed services (snapshot, addon, override, trial repos)
//   - Fake outbound clients: IAM client, Valkey cache, Temporal starter
//   - Internal routes use x-service-token header (no JWT needed)
//   - Health/readiness routes need no auth
//   - Operator routes use gincommon header format (x-user-id, x-tenant-id,
//     x-tenant-roles) — ProtectedMiddlewares validates format, not crypto
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/http"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

func init() { gin.SetMode(gin.TestMode) }

const (
	e2eServiceToken = "e2e-internal-token"
	e2eOperatorRole = "platform_operator"
)

// e2eEnv is the full wired-up server for one e2e test.
type e2eEnv struct {
	srv      *httptest.Server
	tenantID uuid.UUID
	appPool  *pgcommon.Pool
}

// newE2EEnv starts a Postgres container, runs migrations, wires all services
// and handlers, and returns an httptest.Server.
func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("entitlements_e2e"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("e2epassword"),
		tcpostgres.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	superDSN, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, pgadapter.MigrateSchema(ctx, superDSN, nil))

	tenantID := uuid.New()
	appPool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:           superDSN,
		PGBouncerMode: false,
		GUCProvider: func(ctx context.Context) map[string]string {
			if id, ok := ctx.Value(e2eTenantKey{}).(uuid.UUID); ok {
				return map[string]string{"app.tenant_id": id.String()}
			}
			return map[string]string{"app.tenant_id": tenantID.String()}
		},
	})
	require.NoError(t, err)
	t.Cleanup(appPool.Close)

	router := buildE2ERouter(appPool, tenantID)
	srv := httptest.NewServer(router.Handler())
	t.Cleanup(srv.Close)

	return &e2eEnv{srv: srv, tenantID: tenantID, appPool: appPool}
}

type e2eTenantKey struct{}

// withTenantCtx stores tenantID in ctx so the GUC provider picks it up.
func withTenantCtx(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, e2eTenantKey{}, id)
}

// buildE2ERouter wires up all services and handlers using the real postgres
// pool and in-memory fakes for external clients.
func buildE2ERouter(appPool *pgcommon.Pool, _ uuid.UUID) *httpadapter.Router {
	nopLog := port.NopLogger{}
	fakePub := &e2eFakePublisher{}
	txRunner := pgadapter.NewTxRunner(appPool, fakePub)

	snapRepo := pgadapter.NewSnapshotRepository(appPool)
	overrideRepo := pgadapter.NewOverrideRepository(appPool)
	addonRepo := pgadapter.NewAddonRepository(appPool)
	trialRepo := pgadapter.NewTrialRepository(appPool)
	drawdownRepo := pgadapter.NewDrawdownRepository(appPool)
	shortfallRepo := pgadapter.NewShortfallRepository(appPool)

	fakeIAM := &e2eFakeIAMClient{}
	fakeCache := &e2eFakeCache{}
	fakeTemporal := &e2eFakeTemporalStarter{}

	catalogSvc := service.NewCatalogService(fakeIAM, fakeCache, fakeTemporal).WithLogger(nopLog)
	effectSvc := service.NewEffectiveSetService(
		snapRepo, overrideRepo, addonRepo, trialRepo,
		catalogSvc, fakeIAM, fakeCache, txRunner,
	).WithLogger(nopLog)
	overrideSvc := service.NewOverrideService(overrideRepo, effectSvc, txRunner).WithLogger(nopLog)
	trialSvc := service.NewTrialService(trialRepo, effectSvc, txRunner).WithLogger(nopLog)
	addonSvc := service.NewAddonService(addonRepo, drawdownRepo, shortfallRepo, effectSvc, txRunner).WithLogger(nopLog)

	ginCfg := gincommon.Config{
		ServiceName: "commercial-entitlements-e2e",
		Domain:      "commercial",
		Environment: "test",
	}

	return httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig:          ginCfg,
		EntitlementHandler: httpadapter.NewEntitlementHandler(effectSvc),
		OverrideHandler:    httpadapter.NewOverrideHandler(overrideSvc),
		AddonHandler:       httpadapter.NewAddonHandler(addonSvc),
		ShortfallHandler:   httpadapter.NewShortfallHandler(addonSvc),
		TrialHandler:       httpadapter.NewTrialHandler(trialSvc),
		PlansHandler:       httpadapter.NewPlansHandler(catalogSvc),
	})
}

// ── request helpers ────────────────────────────────────────────────────────

type reqOpts struct {
	method  string
	path    string
	headers map[string]string
	body    any
}

func (e *e2eEnv) do(t *testing.T, opts reqOpts) (int, http.Header, []byte) {
	t.Helper()
	var bodyReader io.Reader
	if opts.body != nil {
		b, err := json.Marshal(opts.body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(opts.method, e.srv.URL+opts.path, bodyReader)
	require.NoError(t, err)
	if opts.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range opts.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, body
}

func unmarshalBody(t *testing.T, body []byte, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(body, v))
}

// serviceHeaders returns headers for internal service routes.
func serviceHeaders() map[string]string {
	return map[string]string{"x-service-token": e2eServiceToken}
}

// operatorHeaders returns gincommon identity headers for operator routes.
func operatorHeaders(userID string, tenantID uuid.UUID) map[string]string {
	return map[string]string{
		"x-user-id":      userID,
		"x-tenant-id":    tenantID.String(),
		"x-tenant-roles": e2eOperatorRole,
	}
}

// seedSnapshot inserts a snapshot for the given tenant directly via repo.
func (e *e2eEnv) seedSnapshot(t *testing.T, tenantID uuid.UUID, plan string) {
	t.Helper()
	ctx := withTenantCtx(context.Background(), tenantID)
	repo := pgadapter.NewSnapshotRepository(e.appPool)
	snap := &domain.EntitlementSnapshot{
		TenantID:            tenantID,
		Version:             1,
		SourceRecordVersion: 1,
		Plan:                plan,
		State:               domain.EntitlementStateActive,
		FeatureFlags:        []any{},
		ModelTier:           "standard",
		Limits:              map[domain.LimitKey]int64{domain.LimitKeyLLMTokenQuotaMonthly: 100_000},
		Cause:               domain.EffectiveSetCausePlanChange,
		ComputedAt:          time.Now().UTC(),
	}
	_, err := repo.Upsert(ctx, snap)
	require.NoError(t, err)
}

// ── fakes ──────────────────────────────────────────────────────────────────

type e2eFakePublisher struct{}

func (e *e2eFakePublisher) Enqueue(_ context.Context, _ *domain.DomainEvent) error { return nil }

type e2eFakeIAMClient struct{}

func (f *e2eFakeIAMClient) FetchPlan(_ context.Context, planCode string) (*domain.Plan, error) {
	return &domain.Plan{
		Code:          planCode,
		DisplayName:   "Test Plan",
		ModelTier:     "standard",
		FeatureSet:    map[string]any{},
		TrialProfiles: map[string]domain.TrialProfileLimits{},
	}, nil
}

func (f *e2eFakeIAMClient) FetchTenantSubscription(_ context.Context, tenantID uuid.UUID) (*port.TenantSubscription, error) {
	return &port.TenantSubscription{
		TenantID: tenantID, PlanCode: "starter",
		State: "active", RecordVersion: 1,
	}, nil
}

type e2eFakeCache struct{}

func (c *e2eFakeCache) GetEffectiveSet(_ context.Context, _ uuid.UUID) (*domain.EffectiveSet, error) {
	return nil, nil
}
func (c *e2eFakeCache) SetEffectiveSet(_ context.Context, _ uuid.UUID, _ *domain.EffectiveSet) error {
	return nil
}
func (c *e2eFakeCache) InvalidateEffectiveSet(_ context.Context, _ uuid.UUID) error { return nil }
func (c *e2eFakeCache) SetQuotaCeiling(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ *int64) error {
	return nil
}
func (c *e2eFakeCache) ResetUsageCounter(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ string) error {
	return nil
}
func (c *e2eFakeCache) DeleteUsageCounters(_ context.Context, _ domain.LimitKey, _ string) (int64, error) {
	return 0, nil
}
func (c *e2eFakeCache) GetPlan(_ context.Context, _ string) (*domain.Plan, error) { return nil, nil }
func (c *e2eFakeCache) SetPlan(_ context.Context, _ *domain.Plan) error           { return nil }
func (c *e2eFakeCache) InvalidatePlan(_ context.Context, _ string) error          { return nil }
func (c *e2eFakeCache) Health(_ context.Context) error                            { return nil }
func (c *e2eFakeCache) Close() error                                              { return nil }

type e2eFakeTemporalStarter struct{}

func (f *e2eFakeTemporalStarter) StartCatalogFanout(_ context.Context, _ string) error { return nil }
func (f *e2eFakeTemporalStarter) StartExpiryFanout(_ context.Context) error            { return nil }

// helper: format path with tenant ID.
func tenantPath(tenantID uuid.UUID, suffix string) string {
	return fmt.Sprintf("/api/v1/internal/tenants/%s%s", tenantID, suffix)
}
