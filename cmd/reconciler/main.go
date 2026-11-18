// Package main is the single-binary reconciler entry point (LLD §13.1).
// Selects a job by --job flag or RECONCILER_JOB env var and dispatches it.
// Each Kubernetes CronJob invokes this binary with a different --job value.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"

	eventbusadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/cmd/reconciler/jobs"

	eventcfg "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgmetrics"
	"github.com/prometheus/client_golang/prometheus/push"
)

// buildVersion is injected at build time.
var buildVersion = "dev"

var registry = map[string]jobs.Func{
	"override-expiry":        jobs.OverrideExpiry,
	"addon-expiry":           jobs.AddonExpiry,
	"outbox-prune":           jobs.OutboxPrune,
	"processed-events-prune": jobs.ProcessedEventsPrune,
	"quota-reset":            jobs.QuotaReset,
}

func main() {
	os.Exit(run())
}

func run() int {
	var jobName string
	flag.StringVar(&jobName, "job", os.Getenv("RECONCILER_JOB"), "reconciler job code")
	flag.Parse()

	appEnv := resolveAppEnv()

	rawLog, err := logger.NewLogger(loggerEnv(appEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, "init logger: "+err.Error())
		return 1
	}
	log := port.NewSlogStyleLogger(rawLog)

	if jobName == "" {
		log.Error("no job specified — pass --job=<name> or set RECONCILER_JOB", "valid_jobs", registryNames())
		return 1
	}
	fn, ok := registry[jobName]
	if !ok {
		log.Error("unknown reconciler job", "job", jobName, "valid_jobs", registryNames())
		return 1
	}

	if os.Getenv("SYSTEM_DATABASE_URL") == "" && !isDevLikeEnv(appEnv) {
		log.Error("SYSTEM_DATABASE_URL required outside dev — cross-tenant reconciler sweeps will RLS-filter zero rows without it",
			"app_env", appEnv, "job", jobName)
		return 1
	}

	serviceName := envOr("APP_NAME", "commercial-entitlements")
	bv := envOr("BUILD_VERSION", buildVersion)

	shutdownTracing, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  serviceName,
		BuildVersion: bv,
		Domain:       "commercial",
		Environment:  appEnv,
		Logger:       rawLog,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init tracing: "+err.Error())
		return 1
	}
	port.SetTraceExtractor(func(ctx context.Context) string {
		sc := trace.SpanFromContext(ctx).SpanContext()
		if !sc.IsValid() {
			return ""
		}
		return sc.TraceID().String()
	})
	port.SetSpanContextExtractor(func(ctx context.Context) port.SpanContext {
		sc := trace.SpanFromContext(ctx).SpanContext()
		if !sc.IsValid() {
			return port.SpanContext{}
		}
		return port.SpanContext{TraceID: sc.TraceID().String(), SpanID: sc.SpanID().String()}
	})
	defer func() {
		if err := gincommon.Shutdown(rawLog); err != nil {
			log.Error("logger/tracer flush error", "error", err.Error())
		}
	}()
	defer shutdownTracing()

	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Environment:  appEnv,
		Logger:       rawLog,
		ServiceName:  serviceName,
		BuildVersion: bv,
		Domain:       "commercial",
	})
	metrics.Register(appEnv)
	identity := gincommon.MetricsConstLabels()
	evWarnings, err := events.InitMetrics(events.MetricsIdentityFromLabels(identity), gincommon.MetricsRegisterer())
	for _, w := range evWarnings {
		log.Warn("platform-events metric disabled", "metric", w.Metric, "error", w.Error())
	}
	if err != nil {
		log.Error("init platform-events metrics", "error", err.Error())
		return 1
	}
	pgMetricWarnings, err := pgmetrics.InitWithIdentity(pgmetrics.IdentityFromLabels(identity), gincommon.MetricsRegisterer())
	for _, w := range pgMetricWarnings {
		log.Warn("platform-pgcommon metric disabled", "metric", w.Metric, "error", w.Error())
	}
	if err != nil {
		log.Error("init platform-pgcommon metrics", "error", err.Error())
		return 1
	}

	log.Info("reconciler starting", "job", jobName)

	timeout := 5 * time.Minute
	if s := os.Getenv("RECONCILER_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ctx, jobSpan := gincommon.NewTracer(serviceName).Start(ctx, "reconciler."+jobName)
	defer jobSpan.End()

	pgCfg, pgWarnings := pgcommon.ConfigFromEnv()
	for _, w := range pgWarnings {
		log.Warn("postgres config warning", "key", w.Key, "reason", w.Reason)
	}
	pgCfg.DSN = pgadapter.DSNFromEnv()
	stmtTimeout := envDuration("RECONCILER_STATEMENT_TIMEOUT", defaultReconcilerStatementTimeout)
	pgCfg.StatementTimeout = stmtTimeout
	pgCfg.GUCProvider = pgcommon.GUCSetFromContext
	pgCfg.Logger = pgadapter.NewLoggerAdapter(rawLog)
	pgCfg.Tracer = gincommon.NewSpanTracer(serviceName)
	pool, err := pgcommon.NewPool(ctx, pgCfg)
	if err != nil {
		log.Error("connect postgres", "error", err.Error())
		return 1
	}

	var drainOnce sync.Once
	drainPool := func() {
		drainOnce.Do(func() {
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancelDrain()
			if err := pool.DrainAndClose(drainCtx); err != nil {
				log.Error("pool drain error", "error", err.Error())
			}
		})
	}
	defer drainPool()

	codec := eventbusadapter.Codec(eventbusadapter.NoopCodec{})
	validCodec, err := eventbusadapter.NewValidatingCodec(codec)
	if err != nil {
		log.Error("init validating codec", "error", err.Error())
		return 1
	}
	outboxPublisher := eventbusadapter.New(eventbusadapter.EventSource, validCodec).WithLogger(rawLog)

	outboxEnv := eventcfg.LoadOutbox()
	eventcfg.LogWarningsTo(rawLog, outboxEnv.Warnings)
	outboxRunner, err := outbox.NewRunner(eventcfg.RunnerConfigFromEnv(outboxEnv, pool, eventbusadapter.NoopPublisher{}, rawLog))
	if err != nil {
		log.Error("create outbox runner", "error", err.Error())
		return 1
	}

	txRunner := pgadapter.NewTxRunner(pool, outboxPublisher)
	overrideRepo := pgadapter.NewOverrideRepository(pool)
	addonRepo := pgadapter.NewAddonRepository(pool)
	shortfallRepo := pgadapter.NewShortfallRepository(pool)
	idempotencyRepo := pgadapter.NewIdempotencyRepository(pool, outboxPublisher)

	jctx := &jobs.Context{
		TxRunner:               txRunner,
		Overrides:              overrideRepo,
		Addons:                 addonRepo,
		Shortfalls:             shortfallRepo,
		Inbox:                  idempotencyRepo,
		OutboxRunner:           outboxRunner,
		Logger:                 log,
		BatchLimit:             envInt("RECONCILER_BATCH_LIMIT", 500),
		OutboxRetentionDays:    envInt("OUTBOX_RETENTION_DAYS", 8),
		ProcessedEventsTTLDays: envInt("PROCESSED_EVENTS_TTL_DAYS", 8),
	}

	res, err := fn(ctx, jctx)
	pushMetrics(ctx, jobName, log)
	if err != nil {
		log.Error("reconciler job failed", "job", jobName, "error", err.Error())
		return 1
	}
	log.Info("reconciler complete", "job", jobName,
		"attempted", res.Attempted, "succeeded", res.Succeeded, "failed", res.Failed)
	return 0
}

func pushMetrics(ctx context.Context, jobName string, log port.SlogStyleLogger) {
	url := os.Getenv("PROMETHEUS_PUSHGATEWAY_URL")
	if url == "" {
		return
	}
	pushCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := push.New(url, "commercial-entitlements-reconciler").
		Grouping("cronjob", jobName).
		Gatherer(gincommon.MetricsGatherer()).
		PushContext(pushCtx); err != nil {
		log.Warn("pushgateway push failed", "job", jobName, "error", err.Error())
	}
}

func registryNames() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	return names
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

const defaultReconcilerStatementTimeout = 2 * time.Minute

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
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
