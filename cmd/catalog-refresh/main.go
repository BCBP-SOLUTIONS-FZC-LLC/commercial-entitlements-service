// Package main sweeps all cached plan codes, re-fetches each from the IAM
// catalog service, and triggers CatalogFanoutWorkflow for each one. Run as a
// periodic maintenance job or after a known catalog update.
package main

import (
	"context"
	"fmt"
	"os"

	temporalclient "go.temporal.io/sdk/client"

	eventbusadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	iamclientadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/iamclient"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	valkeyadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/valkey"
	temporaladapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/temporal"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

func main() {
	rawLog, _ := logger.NewLogger("dev")
	log := &slogAdapter{rawLog}

	pgCfg, _ := pgcommon.ConfigFromEnv()
	pgCfg.DSN = pgadapter.DSNFromEnv()
	pgCfg.GUCProvider = pgcommon.GUCSetFromContext
	pgCfg.Logger = pgadapter.NewLoggerAdapter(log)
	pool, err := pgcommon.NewPool(context.Background(), pgCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	cache := valkeyadapter.New(envOr("VALKEY_URL", "localhost:6379"))
	defer cache.Close()

	tempAddr := envOr("TEMPORAL_HOST_PORT", "localhost:7233")
	tc, err := temporalclient.Dial(temporalclient.Options{HostPort: tempAddr})
	if err != nil {
		fmt.Fprintf(os.Stderr, "temporal: %v\n", err)
		os.Exit(1)
	}
	defer tc.Close()

	enqueueCodec, _ := eventbusadapter.NewValidatingCodec(eventbusadapter.NoopCodec{})
	outboxPublisher := eventbusadapter.New(eventbusadapter.EventSource, enqueueCodec)
	txRunner := pgadapter.NewTxRunner(pool, outboxPublisher)
	snapshotRepo := pgadapter.NewSnapshotRepository(pool)
	overrideRepo := pgadapter.NewOverrideRepository(pool)
	addonRepo := pgadapter.NewAddonRepository(pool)
	trialRepo := pgadapter.NewTrialRepository(pool)
	iamClient := iamclientadapter.New(log)
	starter := temporaladapter.NewStarter(tc)
	catalogSvc := service.NewCatalogService(iamClient, cache, starter).WithLogger(log)
	effectSvc := service.NewEffectiveSetService(snapshotRepo, overrideRepo, addonRepo, trialRepo, catalogSvc, iamClient, cache, txRunner)
	_ = effectSvc

	catalogCacheRepo := pgadapter.NewCatalogCacheRepository(pool)
	planCodes, err := catalogCacheRepo.ListPlanCodes(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "list plans: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	for _, code := range planCodes {
		if _, err := catalogSvc.RefreshPlan(ctx, code); err != nil {
			log.Warn("refresh failed", map[string]interface{}{"plan": code, "error": err.Error()})
			continue
		}
		if err := catalogSvc.FanoutCatalogRefresh(ctx, code); err != nil {
			log.Warn("fanout failed", map[string]interface{}{"plan": code, "error": err.Error()})
		} else {
			log.Info("refreshed and fanned out", map[string]interface{}{"plan": code})
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

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
