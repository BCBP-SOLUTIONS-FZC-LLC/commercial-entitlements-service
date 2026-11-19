// Package main triggers the ExpiryFanout Temporal workflow which expires
// overrides and addon packs, then recomputes affected tenants.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	temporalclient "go.temporal.io/sdk/client"

	temporaladapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/temporal"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
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

	log, err := logger.NewLogger(logEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init logger: "+err.Error())
		os.Exit(1)
	}

	serviceName := envOr("APP_NAME", "commercial-entitlements-expiry")
	shutdownTracing, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  serviceName,
		BuildVersion: envOr("BUILD_VERSION", "dev"),
		Domain:       "commercial",
		Environment:  appEnv,
		Logger:       log,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init tracing: "+err.Error())
		os.Exit(1)
	}
	defer shutdownTracing()
	defer func() { _ = gincommon.Shutdown(log) }()

	port.SetTraceExtractor(gincommon.SpanTraceID)

	tempAddr := envOr("TEMPORAL_HOST_PORT", "localhost:7233")
	tc, err := temporalclient.Dial(temporalclient.Options{HostPort: tempAddr})
	if err != nil {
		log.Error("connect temporal", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	defer tc.Close()

	starter := temporaladapter.NewStarter(tc)
	if err := starter.StartExpiryFanout(context.Background()); err != nil {
		log.Error("expiry fanout failed", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	log.Info("expiry fanout workflow started", nil)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
