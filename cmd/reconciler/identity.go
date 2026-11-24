package main

import (
	"os"
	"strings"
)

// resolveAppEnv returns the one environment name this process uses for
// metrics, tracing, logging and its own dev/prod checks.
func resolveAppEnv() string {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT"} {
		if v := strings.ToLower(strings.TrimSpace(os.Getenv(key))); v != "" {
			return v
		}
	}
	return "dev"
}

// isDevLikeEnv reports whether appEnv is a local/dev alias.
func isDevLikeEnv(appEnv string) bool {
	switch appEnv {
	case "dev", "local", "test":
		return true
	default:
		return false
	}
}

// loggerEnv maps appEnv onto the gincommon logger environment value.
func loggerEnv(appEnv string) string {
	if isDevLikeEnv(appEnv) {
		return "dev"
	}
	return appEnv
}
