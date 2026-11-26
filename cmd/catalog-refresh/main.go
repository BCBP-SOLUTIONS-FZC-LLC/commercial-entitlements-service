// Package main sweeps all cached plan codes, re-fetches each from the IAM
// catalog service, and triggers CatalogFanoutWorkflow for each one. Run as a
// periodic maintenance job or after a known catalog update.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/sqspipeline"
	eventbusadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	iamclientadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/iamclient"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	valkeyadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/valkey"
	temporaladapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/temporal"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgmetrics"
)

func main() {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if appEnv == "" {
		appEnv = "dev"
	}
	logEnv := appEnv
	if appEnv == "local" || appEnv == "dev" || appEnv == "test" {
		logEnv = "dev"
	}

	rawLog, err := logger.NewLogger(logEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init logger: "+err.Error())
		os.Exit(1)
	}

	cfg := gincommon.Config{
		Logger:       rawLog,
		ServiceName:  envOr("APP_NAME", "commercial-entitlements-catalog-refresh"),
		BuildVersion: envOr("BUILD_VERSION", "dev"),
		Domain:       "commercial",
		Environment:  appEnv,
	}

	shutdownTracing, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  cfg.ServiceName,
		BuildVersion: cfg.BuildVersion,
		Domain:       cfg.Domain,
		Environment:  cfg.Environment,
		Logger:       rawLog,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init tracing: "+err.Error())
		os.Exit(1)
	}
	defer shutdownTracing()
	defer func() { _ = gincommon.Shutdown(rawLog) }()

	_ = gincommon.ObservabilityMiddlewares(cfg)
	sqspipeline.InstallTracingHooks(cfg.ServiceName)

	identity := gincommon.MetricsConstLabels()
	evWarnings, err := events.InitMetrics(events.MetricsIdentityFromLabels(identity), gincommon.MetricsRegisterer())
	for _, w := range evWarnings {
		rawLog.Warn("platform-events metric disabled", map[string]any{"metric": w.Metric, "error": w.Error()})
	}
	if err != nil {
		panic(fmt.Sprintf("init platform-events metrics: %v", err))
	}
	pgMetricWarnings, err := pgmetrics.InitWithIdentity(pgmetrics.IdentityFromLabels(identity), gincommon.MetricsRegisterer())
	for _, w := range pgMetricWarnings {
		rawLog.Warn("platform-pgcommon metric disabled", map[string]any{"metric": w.Metric, "error": w.Error()})
	}
	if err != nil {
		panic(fmt.Sprintf("init platform-pgcommon metrics: %v", err))
	}

	pgCfg, pgCfgWarnings := pgcommon.ConfigFromEnv()
	for _, w := range pgCfgWarnings {
		rawLog.Warn("postgres config warning", map[string]any{"key": w.Key, "reason": w.Reason})
	}
	pgCfg.DSN = pgadapter.DSNFromEnv()
	pgCfg.GUCProvider = pgcommon.GUCSetFromContext
	pgCfg.Logger = pgadapter.NewLoggerAdapter(rawLog)
	pgCfg.Tracer = gincommon.NewSpanTracer(cfg.ServiceName)
	pool, err := pgcommon.NewPool(context.Background(), pgCfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect postgres: "+err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	cache := valkeyadapter.New(envOr("VALKEY_URL", "localhost:6379")).WithLogger(rawLog)
	defer func() { _ = cache.Close() }()

	tempAddr := envOr("TEMPORAL_HOST_PORT", "localhost:7233")
	tc, err := temporalclient.Dial(temporalclient.Options{HostPort: tempAddr})
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect temporal: "+err.Error())
		os.Exit(1)
	}
	defer tc.Close()

	enqueueCodec, _ := eventbusadapter.NewValidatingCodec(eventbusadapter.NoopCodec{})
	outboxPublisher := eventbusadapter.New(eventbusadapter.EventSource, enqueueCodec).WithLogger(rawLog)
	txRunner := pgadapter.NewTxRunner(pool, outboxPublisher)
	snapshotRepo := pgadapter.NewSnapshotRepository(pool)
	overrideRepo := pgadapter.NewOverrideRepository(pool)
	addonRepo := pgadapter.NewAddonRepository(pool)
	trialRepo := pgadapter.NewTrialRepository(pool)
	iamClient := iamclientadapter.New(rawLog)
	starter := temporaladapter.NewStarter(tc)
	catalogCacheRepo := pgadapter.NewCatalogCacheRepository(pool)
	catalogSvc := service.NewCatalogService(iamClient, cache, starter).
		WithLogger(rawLog).
		WithCatalogCache(catalogCacheRepo)
	effectSvc := service.NewEffectiveSetService(snapshotRepo, overrideRepo, addonRepo, trialRepo, catalogSvc, iamClient, cache, txRunner)
	_ = effectSvc
	ctx := context.Background()
	planCodes, err := catalogCacheRepo.ListPlanCodes(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list plans: "+err.Error())
		os.Exit(1)
	}

	// Bootstrap: when plan_catalog_cache is empty (first deploy / fresh DB),
	// fetch all plans from iam-catalog-admin via CAT-I2 bulk and seed the DB+Valkey
	// so the per-code refresh loop below has codes to work with.
	if len(planCodes) == 0 {
		rawLog.Info("plan_catalog_cache empty — bootstrapping from IAM catalog", nil)
		allPlans, fetchErr := iamClient.FetchAllPlans(ctx)
		if fetchErr != nil {
			rawLog.Warn("bootstrap FetchAllPlans failed — no plans to refresh", map[string]any{"error": fetchErr.Error()})
		} else {
			for _, p := range allPlans {
				if setErr := catalogCacheRepo.SetPlan(ctx, p); setErr != nil {
					rawLog.Warn("bootstrap SetPlan DB failed", map[string]any{"plan": p.Code, "error": setErr.Error()})
					continue
				}
				_ = cache.SetPlan(ctx, p)
				planCodes = append(planCodes, p.Code)
				rawLog.Info("bootstrapped plan", map[string]any{"plan": p.Code})
			}
		}
	}

	for _, code := range planCodes {
		if _, err := catalogSvc.RefreshPlan(ctx, code); err != nil {
			rawLog.Warn("refresh failed", map[string]any{"plan": code, "error": err.Error()})
			continue
		}
		if err := catalogSvc.FanoutCatalogRefresh(ctx, code); err != nil {
			rawLog.Warn("fanout failed", map[string]any{"plan": code, "error": err.Error()})
		} else {
			rawLog.Info("refreshed and fanned out", map[string]any{"plan": code})
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
