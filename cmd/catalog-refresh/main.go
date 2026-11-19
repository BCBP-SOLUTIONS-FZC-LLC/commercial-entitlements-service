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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
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

	pgCfg, _ := pgcommon.ConfigFromEnv()
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
	defer cache.Close()

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
	catalogSvc := service.NewCatalogService(iamClient, cache, starter).WithLogger(rawLog)
	effectSvc := service.NewEffectiveSetService(snapshotRepo, overrideRepo, addonRepo, trialRepo, catalogSvc, iamClient, cache, txRunner)
	_ = effectSvc

	catalogCacheRepo := pgadapter.NewCatalogCacheRepository(pool)
	planCodes, err := catalogCacheRepo.ListPlanCodes(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "list plans: "+err.Error())
		os.Exit(1)
	}

	ctx := context.Background()
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
