// Package main is the composition root for the commercial-entitlements server.
// It wires pgcommon.NewPool + GUC provider, outbox runner, auditoutbox runner,
// SQS consumers, Temporal starter, and the HTTP surface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/gin-gonic/gin"
	temporalclient "go.temporal.io/sdk/client"

	consumeradapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/consumer"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/sqspipeline"
	auditrecorderadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/auditrecorder"
	eventbusadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	iamclientadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/iamclient"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	valkeyadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/valkey"
	temporaladapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/temporal"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-audit/pkg/audit"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-audit/pkg/auditoutbox"
	eventcfg "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgmetrics"
)

// buildVersion is injected by -ldflags at build time.
var buildVersion = "dev"

const requestTimeout = 30 * time.Second

func main() {
	appEnv := resolveAppEnv()
	if !isDevLikeAppEnv(appEnv) {
		gin.SetMode(gin.ReleaseMode)
	}

	// ── 1. Logger ────────────────────────────────────────────────────────
	log, err := logger.NewLogger(loggerEnv(appEnv))
	if err != nil {
		panic("init logger: " + err.Error())
	}

	cfg := gincommon.Config{
		Logger:         log,
		ServiceName:    envOr("APP_NAME", "commercial-entitlements"),
		BuildVersion:   envOr("BUILD_VERSION", buildVersion),
		Domain:         "commercial",
		Environment:    appEnv,
		RequestTimeout: requestTimeout,
	}

	// ── 2. Tracing ───────────────────────────────────────────────────────
	shutdownTracing, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  cfg.ServiceName,
		BuildVersion: cfg.BuildVersion,
		Domain:       cfg.Domain,
		Environment:  cfg.Environment,
		Logger:       log,
	})
	if err != nil {
		panic("init tracing: " + err.Error())
	}

	sqspipeline.InstallTracingHooks(cfg.ServiceName)

	_ = gincommon.ObservabilityMiddlewares(cfg)
	metrics.Register(appEnv)

	identity := gincommon.MetricsConstLabels()
	evWarnings, err := events.InitMetrics(events.MetricsIdentityFromLabels(identity), gincommon.MetricsRegisterer())
	for _, w := range evWarnings {
		log.Warn("platform-events metric disabled", map[string]interface{}{"metric": w.Metric, "error": w.Error()})
	}
	if err != nil {
		panic(fmt.Sprintf("init platform-events metrics: %v", err))
	}
	pgWarnings, err := pgmetrics.InitWithIdentity(pgmetrics.IdentityFromLabels(identity), gincommon.MetricsRegisterer())
	for _, w := range pgWarnings {
		log.Warn("platform-pgcommon metric disabled", map[string]interface{}{"metric": w.Metric, "error": w.Error()})
	}
	if err != nil {
		panic(fmt.Sprintf("init platform-pgcommon metrics: %v", err))
	}
	auditWarnings, err := audit.InitMetrics(audit.MetricsIdentity{
		Domain: identity["domain"], Service: identity["service"], Environment: identity["environment"],
	}, gincommon.MetricsRegisterer())
	for _, w := range auditWarnings {
		log.Warn("platform-audit metric disabled", map[string]interface{}{"metric": w.Metric, "error": w.Error()})
	}
	if err != nil {
		panic(fmt.Sprintf("init platform-audit metrics: %v", err))
	}

	// ── 3. Database ──────────────────────────────────────────────────────
	pgCfg, pgCfgWarnings := pgcommon.ConfigFromEnv()
	for _, w := range pgCfgWarnings {
		log.Warn("postgres config warning", map[string]interface{}{"key": w.Key, "reason": w.Reason})
	}
	dsn := pgadapter.DSNFromEnv()
	migrationDSN := pgadapter.MigrationDSNFromEnv()
	pgCfg.DSN = dsn
	pgCfg.GUCProvider = pgcommon.GUCSetFromContext
	pgCfg.Logger = pgadapter.NewLoggerAdapter(log)
	pgCfg.Tracer = gincommon.NewSpanTracer(cfg.ServiceName)

	pool, err := pgcommon.NewPool(context.Background(), pgCfg)
	if err != nil {
		panic(fmt.Sprintf("connect postgres: %v", err))
	}
	defer pool.Close()

	ctx, cancelBackground := context.WithCancel(context.Background())
	defer cancelBackground()

	// Schema migration (unless RUN_MIGRATIONS=false).
	if runMigrationsAtStartup() {
		if err := pgadapter.MigrateSchema(ctx, migrationDSN, log); err != nil {
			panic(fmt.Sprintf("migrate: %v", err))
		}
	} else {
		log.Info("RUN_MIGRATIONS=false — schema managed by migrate Job", nil)
	}

	// ── 4. Cache (Valkey) ────────────────────────────────────────────────
	cache := valkeyadapter.New(envOr("VALKEY_URL", "localhost:6379")).WithLogger(log)
	defer func() {
		if cerr := cache.Close(); cerr != nil {
			log.Error("valkey close error", map[string]interface{}{"error": cerr.Error()})
		}
	}()

	// ── 5. AWS clients ───────────────────────────────────────────────────
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(envOr("AWS_REGION", "ap-south-1")))
	if err != nil {
		panic(fmt.Sprintf("load aws config: %v", err))
	}
	var sqsOpts []func(*sqs.Options)
	var glueOpts []func(*glue.Options)
	if ep := os.Getenv("AWS_ENDPOINT_URL"); ep != "" {
		sqsOpts = append(sqsOpts, func(o *sqs.Options) { o.BaseEndpoint = &ep })
		glueOpts = append(glueOpts, func(o *glue.Options) { o.BaseEndpoint = &ep })
	}
	sqsClient := sqs.NewFromConfig(awsCfg, sqsOpts...)
	dlqPublisher, err := events.NewSQSDLQPublisherWithClient(events.DLQConfig{
		ConsumerName: envOr("APP_NAME", "commercial-entitlements"),
		Logger:       log,
	}, sqsClient)
	if err != nil {
		panic(fmt.Sprintf("build DLQ publisher: %v", err))
	}
	glueClient := glue.NewFromConfig(awsCfg, glueOpts...)

	// ── 6. Event codec + outbox publisher ───────────────────────────────
	enqueueCodec, err := eventbusadapter.NewValidatingCodec(eventbusadapter.NoopCodec{})
	if err != nil {
		panic(fmt.Sprintf("init validating codec: %v", err))
	}
	outboxPublisher := eventbusadapter.New(eventbusadapter.EventSource, enqueueCodec).WithLogger(log)
	consumedValidator := validatorFunc(func(eventType string, payload []byte) error {
		if err := enqueueCodec.Validate(eventType, payload); !errors.Is(err, eventbusadapter.ErrNoSchema) {
			return err
		}
		return nil
	})
	txRunner := pgadapter.NewTxRunner(pool, outboxPublisher)

	// ── 6b. Audit outbox ─────────────────────────────────────────────────
	auditCfg, err := auditrecorderadapter.LoadConfig()
	if err != nil {
		panic(fmt.Sprintf("audit config: %v", err))
	}
	if err := auditoutbox.CheckEnqueueAccess(ctx, pool); err != nil {
		panic(fmt.Sprintf("audit enqueue access check: %v", err))
	}
	auditEnq, err := auditoutbox.NewEnqueuer(auditCfg.EnqueueConfig(log))
	if err != nil {
		panic(fmt.Sprintf("audit enqueuer: %v", err))
	}
	auditRecorder := auditrecorderadapter.New(auditEnq)
	txRunner.WithAuditRecorder(auditRecorder)

	var auditRunner *auditoutbox.Runner
	auditClient, err := auditCfg.NewClient(log)
	if err != nil {
		panic(fmt.Sprintf("audit client: %v", err))
	}
	if auditClient != nil {
		auditRunner, err = auditoutbox.NewRunner(auditCfg.RunnerConfig(pool, auditClient, auditEnq, log))
		if err != nil {
			panic(fmt.Sprintf("audit runner: %v", err))
		}
	}

	// SNS routing publisher (single entitlements topic).
	allSchemaNames, err := eventbusadapter.AllSchemaNames()
	if err != nil {
		panic(fmt.Sprintf("list embedded event schemas: %v", err))
	}
	topicCodec, err := buildTopicCodec(ctx, glueClient, os.Getenv("GLUE_REGISTRY_ENTITLEMENTS_NAME"), allSchemaNames)
	if err != nil {
		panic(fmt.Sprintf("init entitlements glue codec: %v", err))
	}
	topicPub, err := buildTopicPublisher(os.Getenv("SNS_TOPIC_ENTITLEMENTS_ARN"), topicCodec, log)
	if err != nil {
		panic(fmt.Sprintf("build entitlements SNS publisher: %v", err))
	}

	// ── 7. Outbox runner ─────────────────────────────────────────────────
	outboxEnv := eventcfg.LoadOutbox()
	eventcfg.LogWarningsTo(log, outboxEnv.Warnings)
	outboxRunner, err := outbox.NewRunner(eventcfg.RunnerConfigFromEnv(outboxEnv, pool, topicPub, log))
	if err != nil {
		panic(fmt.Sprintf("create outbox runner: %v", err))
	}
	go func() {
		if err := outboxRunner.Start(ctx); err != nil {
			log.Error("outbox runner stopped", map[string]interface{}{"error": err.Error()})
		}
	}()
	if auditRunner != nil {
		go func() {
			if err := auditRunner.Start(ctx); err != nil && !errors.Is(err, auditoutbox.ErrRunnerStopped) {
				log.Error("audit runner stopped", map[string]interface{}{"error": err.Error()})
			}
		}()
	}

	// ── 8. Repositories, services, handlers ──────────────────────────────
	snapshotRepo := pgadapter.NewSnapshotRepository(pool)
	overrideRepo := pgadapter.NewOverrideRepository(pool)
	addonRepo := pgadapter.NewAddonRepository(pool)
	drawdownRepo := pgadapter.NewDrawdownRepository(pool)
	shortfallRepo := pgadapter.NewShortfallRepository(pool)
	trialRepo := pgadapter.NewTrialRepository(pool)
	idempotencyRepo := pgadapter.NewIdempotencyRepository(pool, outboxPublisher)

	iamClient := iamclientadapter.New(log)

	// Temporal starter (optional — nil if not configured).
	var temporalStarter port.TemporalStarter
	if temporalAddr := os.Getenv("TEMPORAL_HOST_PORT"); temporalAddr != "" {
		tc, err := temporalclient.Dial(temporalclient.Options{HostPort: temporalAddr})
		if err != nil {
			log.Warn("temporal dial failed — fanout workflows disabled", map[string]interface{}{"error": err.Error()})
		} else {
			temporalStarter = temporaladapter.NewStarter(tc)
		}
	}

	catalogCacheRepo := pgadapter.NewCatalogCacheRepository(pool)
	catalogSvc := service.NewCatalogService(iamClient, cache, temporalStarter).
		WithLogger(log).
		WithCatalogCache(catalogCacheRepo)
	effectSvc := service.NewEffectiveSetService(snapshotRepo, overrideRepo, addonRepo, trialRepo, catalogSvc, iamClient, cache, txRunner).WithLogger(log)
	overrideSvc := service.NewOverrideService(overrideRepo, effectSvc, txRunner).WithLogger(log)
	addonSvc := service.NewAddonService(addonRepo, drawdownRepo, shortfallRepo, effectSvc, txRunner).WithLogger(log)
	trialSvc := service.NewTrialService(trialRepo, effectSvc, txRunner).WithLogger(log)

	entitlementH := httpadapter.NewEntitlementHandler(effectSvc)
	overrideH := httpadapter.NewOverrideHandler(overrideSvc)
	addonH := httpadapter.NewAddonHandler(addonSvc)
	shortfallH := httpadapter.NewShortfallHandler(addonSvc)
	trialH := httpadapter.NewTrialHandler(trialSvc)
	plansH := httpadapter.NewPlansHandler(catalogSvc)

	// ── 7b. Inbound SQS consumers ────────────────────────────────────────
	type wiredConsumer struct {
		cons events.Consumer
	}
	var sqsConsumers []wiredConsumer
	sqsEnv := eventcfg.LoadSQS()
	eventcfg.LogWarningsTo(log, sqsEnv.Warnings)

	billingConsumer := consumeradapter.NewBillingEventConsumer(
		pgadapter.NewIdempotencyRepository(pool, outboxPublisher),
		effectSvc, trialSvc, log)
	tenantConsumer := consumeradapter.NewTenantEventConsumer(
		pgadapter.NewIdempotencyRepository(pool, outboxPublisher),
		effectSvc, trialSvc, log)
	catalogConsumer := consumeradapter.NewCatalogConsumer(
		idempotencyRepo, catalogSvc, cache, log)
	membershipConsumer := consumeradapter.NewMembershipConsumer(
		pgadapter.NewIdempotencyRepository(pool, outboxPublisher),
		effectSvc, trialSvc, log)

	for _, wiring := range []struct {
		envKey   string
		concEnv  string
		name     string
		handleFn func(context.Context, events.Envelope[json.RawMessage]) error
	}{
		{"SQS_BILLING_ENTITLEMENTS_QUEUE_URL", "SQS_BILLING_CONCURRENCY", "billing-entitlements-q", billingConsumer.Handle},
		{"SQS_TENANT_ENTITLEMENTS_QUEUE_URL", "SQS_TENANT_CONCURRENCY", "tenant-entitlements-q", tenantConsumer.Handle},
		{"SQS_CATALOG_ENTITLEMENTS_QUEUE_URL", "SQS_CATALOG_CONCURRENCY", "catalog-entitlements-q", catalogConsumer.Handle},
		{"SQS_MEMBERSHIP_ENTITLEMENTS_QUEUE_URL", "SQS_MEMBERSHIP_CONCURRENCY", "membership-entitlements-q", membershipConsumer.Handle},
	} {
		url := os.Getenv(wiring.envKey)
		if url == "" {
			log.Warn(wiring.envKey+" unset — consumer disabled", map[string]interface{}{"queue": wiring.name})
			continue
		}
		if err := sqspipeline.RequireDLQ(ctx, dlqPublisher, url, log); err != nil {
			panic(fmt.Sprintf("DLQ preflight %s: %v", wiring.name, err))
		}
		cons, err := sqspipeline.NewConsumer(
			sqspipeline.EnvForQueue(sqsEnv, url, envInt(wiring.concEnv, 2)),
			sqsClient,
			sqspipeline.Handler(wiring.name, wiring.handleFn, consumedValidator, dlqPublisher, log),
			dlqPublisher,
			log,
		)
		if err != nil {
			panic(fmt.Sprintf("build consumer %s: %v", wiring.name, err))
		}
		sqsConsumers = append(sqsConsumers, wiredConsumer{cons: cons})
		log.Info("consumer wired", map[string]interface{}{"queue": wiring.name, "url": url})
	}

	for _, wc := range sqsConsumers {
		wc := wc
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("SQS consumer panic", map[string]interface{}{"panic": fmt.Sprintf("%v", r)})
				}
			}()
			if err := wc.cons.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("SQS consumer stopped", map[string]interface{}{"error": err.Error()})
			}
		}()
	}

	// ── Router ───────────────────────────────────────────────────────────
	docsEnabled := envOr("DOCS_ENABLED", "false") == "true"
	docsAuthToken := os.Getenv("DOCS_AUTH_TOKEN")
	if docsEnabled && docsAuthToken == "" && (appEnv == "prod" || appEnv == "staging") {
		panic("startup aborted — DOCS_ENABLED requires DOCS_AUTH_TOKEN in production/staging")
	}

	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig: cfg,
		Docs: httpadapter.DocsConfig{
			Environment: appEnv,
			Enabled:     docsEnabled,
			AuthToken:   docsAuthToken,
		},
		EntitlementHandler: entitlementH,
		OverrideHandler:    overrideH,
		AddonHandler:       addonH,
		ShortfallHandler:   shortfallH,
		TrialHandler:       trialH,
		PlansHandler:       plansH,
		Postgres: pingerFunc(func(ctx context.Context) error {
			if hs := pool.Health(ctx); !hs.Healthy {
				return fmt.Errorf("database not healthy")
			}
			return nil
		}),
		Cache: cache,
		Outbox: pingerFunc(func(context.Context) error {
			select {
			case <-outboxRunner.Ready():
				return nil
			default:
				return fmt.Errorf("outbox initialising")
			}
		}),
		Liveness: func() []httpadapter.Pinger {
			if auditRunner == nil {
				return nil
			}
			return []httpadapter.Pinger{pingerFunc(func(context.Context) error { return auditRunner.Alive() })}
		}(),
	})

	// ── 9. Graceful shutdown ─────────────────────────────────────────────
	srv := &http.Server{
		Addr:              ":" + envOr("APP_PORT", "8080"),
		Handler:           router.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", gincommon.MetricsHandler())
	metricsServer := &http.Server{
		Addr:              ":" + envOr("METRICS_PORT", "9090"),
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	log.Info("service starting", map[string]interface{}{
		"service": cfg.ServiceName,
		"version": cfg.BuildVersion,
		"env":     appEnv,
		"addr":    srv.Addr,
	})
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", map[string]interface{}{"error": err.Error()})
		}
	}()
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server error", map[string]interface{}{"error": err.Error()})
		}
	}()

	<-quit
	log.Info("shutdown signal received — draining", nil)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpDrainTimeout)
	defer cancel()
	var drain sync.WaitGroup
	drain.Add(1)
	go func() {
		defer drain.Done()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("HTTP shutdown error", map[string]interface{}{"error": err.Error()})
		}
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			log.Error("metrics shutdown error", map[string]interface{}{"error": err.Error()})
		}
	}()
	for _, wc := range sqsConsumers {
		drain.Add(1)
		go func() {
			defer drain.Done()
			if err := wc.cons.Stop(); err != nil {
				log.Error("SQS consumer stop error", map[string]interface{}{"error": err.Error()})
			}
		}()
	}
	drain.Wait()

	var runners sync.WaitGroup
	if auditRunner != nil {
		runners.Add(1)
		go func() {
			defer runners.Done()
			auditCtx, cancelAudit := context.WithTimeout(context.Background(), auditStopTimeout)
			defer cancelAudit()
			if err := auditRunner.Stop(auditCtx); err != nil {
				log.Error("audit runner stop error", map[string]interface{}{"error": err.Error()})
			}
		}()
	}
	if err := outboxRunner.Stop(); err != nil {
		log.Error("outbox runner drain error", map[string]interface{}{"error": err.Error()})
	}
	runners.Wait()
	cancelBackground()

	poolCtx, cancelPool := context.WithTimeout(context.Background(), poolDrainTimeout)
	defer cancelPool()
	if err := pool.DrainAndClose(poolCtx); err != nil {
		log.Error("pool drain error", map[string]interface{}{"error": err.Error()})
	}
	shutdownTracing()
	if err := gincommon.Shutdown(log); err != nil {
		log.Error("logger/tracer flush error", map[string]interface{}{"error": err.Error()})
	}
}

const (
	httpDrainTimeout = 30 * time.Second
	poolDrainTimeout = 10 * time.Second
	auditStopTimeout = 10 * time.Second
)

type pingerFunc func(context.Context) error

func (f pingerFunc) Health(ctx context.Context) error { return f(ctx) }

func runMigrationsAtStartup() bool {
	v := strings.TrimSpace(os.Getenv("RUN_MIGRATIONS"))
	if v == "" {
		return true
	}
	run, err := strconv.ParseBool(v)
	return err != nil || run
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// validatorFunc adapts a plain func to sqspipeline.ValidatorFunc.
type validatorFunc func(eventType string, payload []byte) error

func (f validatorFunc) Validate(eventType string, payload []byte) error {
	return f(eventType, payload)
}
