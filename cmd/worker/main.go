// Package main is the Temporal worker for the commercial-entitlements service.
// It registers CatalogFanoutWorkflow and ExpiryFanoutWorkflow.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

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
	log := &slogAdapter{rawLog}

	cfg := gincommon.Config{
		Logger:       rawLog,
		ServiceName:  envOr("APP_NAME", "commercial-entitlements-worker"),
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

	pgCfg, _ := pgcommon.ConfigFromEnv()
	pgCfg.DSN = pgadapter.DSNFromEnv()
	pgCfg.GUCProvider = pgcommon.GUCSetFromContext
	pgCfg.Logger = pgadapter.NewLoggerAdapter(log)
	pgCfg.Tracer = gincommon.NewSpanTracer(cfg.ServiceName)
	pool, err := pgcommon.NewPool(context.Background(), pgCfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect postgres: "+err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	cache := valkeyadapter.New(envOr("VALKEY_URL", "localhost:6379")).WithLogger(log)
	defer cache.Close()

	enqueueCodec, _ := eventbusadapter.NewValidatingCodec(eventbusadapter.NoopCodec{})
	outboxPublisher := eventbusadapter.New(eventbusadapter.EventSource, enqueueCodec)
	txRunner := pgadapter.NewTxRunner(pool, outboxPublisher)

	snapshotRepo := pgadapter.NewSnapshotRepository(pool)
	overrideRepo := pgadapter.NewOverrideRepository(pool)
	addonRepo := pgadapter.NewAddonRepository(pool)
	drawdownRepo := pgadapter.NewDrawdownRepository(pool)
	shortfallRepo := pgadapter.NewShortfallRepository(pool)
	trialRepo := pgadapter.NewTrialRepository(pool)
	iamClient := iamclientadapter.New(log)
	catalogSvc := service.NewCatalogService(iamClient, cache, nil)
	effectSvc := service.NewEffectiveSetService(snapshotRepo, overrideRepo, addonRepo, trialRepo, catalogSvc, iamClient, cache, txRunner)
	overrideSvc := service.NewOverrideService(overrideRepo, effectSvc, txRunner)
	addonSvc := service.NewAddonService(addonRepo, drawdownRepo, shortfallRepo, effectSvc, txRunner)

	tempAddr := envOr("TEMPORAL_HOST_PORT", "localhost:7233")
	tc, err := temporalclient.Dial(temporalclient.Options{HostPort: tempAddr})
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect temporal: "+err.Error())
		os.Exit(1)
	}
	defer tc.Close()

	acts := temporaladapter.NewActivities(effectSvc, overrideSvc, addonSvc, snapshotRepo)

	w := worker.New(tc, temporaladapter.TaskQueue, worker.Options{})
	w.RegisterWorkflow(temporaladapter.CatalogFanoutWorkflow)
	w.RegisterWorkflow(temporaladapter.ExpiryFanoutWorkflow)
	w.RegisterActivity(acts)

	log.Info("temporal worker starting", map[string]interface{}{"queue": temporaladapter.TaskQueue})
	if err := w.Run(worker.InterruptCh()); err != nil {
		fmt.Fprintf(os.Stderr, "worker stopped: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// slogAdapter bridges the gincommon logger to port.Logger interface.
type slogAdapter struct {
	inner interface {
		Info(msg string, args ...interface{})
		Warn(msg string, args ...interface{})
		Error(msg string, args ...interface{})
		Debug(msg string, args ...interface{})
	}
}

func (a *slogAdapter) Info(msg string, fields map[string]interface{})  { a.inner.Info(msg, fields) }
func (a *slogAdapter) Warn(msg string, fields map[string]interface{})  { a.inner.Warn(msg, fields) }
func (a *slogAdapter) Error(msg string, fields map[string]interface{}) { a.inner.Error(msg, fields) }
func (a *slogAdapter) Debug(msg string, fields map[string]interface{}) { a.inner.Debug(msg, fields) }
