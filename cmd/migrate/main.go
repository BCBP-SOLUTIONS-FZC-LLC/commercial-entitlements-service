// Package main is the schema migration entry point for the commercial-entitlements
// service. It applies outbox schema, audit outbox schema, and domain migrations
// exactly once per release via a Kubernetes Job (Helm pre-install/pre-upgrade hook).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
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

	rawLog, err := logger.NewLogger(logEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init logger: "+err.Error())
		os.Exit(1)
	}
	log := port.NewSlogStyleLogger(rawLog)

	migrationDSN := pgadapter.MigrationDSNFromEnv()
	if migrationDSN == "" {
		log.Error("no database DSN — set DATABASE_URL or PG_HOST/PG_USER/PG_PASSWORD")
		os.Exit(1)
	}

	ctx := context.Background()
	log.Info("starting schema migration", "dsn_set", true)

	if err := pgadapter.MigrateSchema(ctx, migrationDSN, rawLog); err != nil {
		log.Error("migration failed", "error", err.Error())
		os.Exit(1)
	}
	log.Info("migration completed successfully")
}
