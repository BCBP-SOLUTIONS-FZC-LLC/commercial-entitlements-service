package main

import (
	"os"
	"strconv"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

// loadShortfallConfig reads the five SHORTFALL_* env vars defined in LLD §12
// and ENT-D12. If a var is absent or unparseable the LLD-approved default is
// used. All values are adjustable after production review without a code change.
func loadShortfallConfig() service.ShortfallConfig {
	cfg := service.DefaultShortfallConfig()
	if v, err := parseInt64Env("SHORTFALL_TOLERANCE_TOKENS"); err == nil {
		cfg.ToleranceTokens = v
	}
	if v, err := parseFloat64Env("SHORTFALL_TOLERANCE_PCT"); err == nil {
		cfg.TolerancePct = v
	}
	if v, err := parseInt64Env("SHORTFALL_HIGH_VALUE_TOKENS"); err == nil {
		cfg.HighValueTokens = v
	}
	if v, err := parseIntEnv("SHORTFALL_REPEAT_COUNT"); err == nil {
		cfg.RepeatCount = v
	}
	if v, err := parseIntEnv("SHORTFALL_REPEAT_WINDOW_MONTHS"); err == nil {
		cfg.RepeatWindowMonths = v
	}
	return cfg
}

func parseInt64Env(key string) (int64, error) {
	return strconv.ParseInt(os.Getenv(key), 10, 64)
}

func parseFloat64Env(key string) (float64, error) {
	return strconv.ParseFloat(os.Getenv(key), 64)
}

func parseIntEnv(key string) (int, error) {
	v, err := strconv.Atoi(os.Getenv(key))
	return v, err
}
