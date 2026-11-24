package main

import (
	"os"
	"strings"
)

// resolveAppEnv returns the one environment name this process uses for
// everything: metrics, tracing, logging and its own dev/prod checks. It uses
// the same precedence the platform libraries use on their own (APP_ENV, then
// ENVIRONMENT; trimmed and lowercased), defaulting to "dev". Passing the
// result explicitly to gincommon keeps metrics and traces from resolving it
// separately and disagreeing.
func resolveAppEnv() string {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT"} {
		if v := strings.ToLower(strings.TrimSpace(os.Getenv(key))); v != "" {
			return v
		}
	}
	return "dev"
}

// isDevLikeAppEnv reports whether appEnv is one of the developer environments.
func isDevLikeAppEnv(appEnv string) bool {
	switch appEnv {
	case "local", "dev", "test":
		return true
	}
	return false
}

// loggerEnv maps appEnv onto the value gincommon's logger understands.
// gincommon treats only "dev"/"local" as development, so "test" (CI and
// local test runs) would otherwise get production JSON logs.
func loggerEnv(appEnv string) string {
	if isDevLikeAppEnv(appEnv) {
		return "dev"
	}
	return appEnv
}
