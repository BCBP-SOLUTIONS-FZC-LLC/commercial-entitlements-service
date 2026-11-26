package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveAppEnv_UsesAPP_ENV(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	assert.Equal(t, "production", resolveAppEnv())
}

func TestResolveAppEnv_FallsBackToENVIRONMENT(t *testing.T) {
	_ = os.Unsetenv("APP_ENV")
	t.Setenv("ENVIRONMENT", "staging")
	assert.Equal(t, "staging", resolveAppEnv())
}

func TestResolveAppEnv_DefaultsDev(t *testing.T) {
	_ = os.Unsetenv("APP_ENV")
	_ = os.Unsetenv("ENVIRONMENT")
	assert.Equal(t, "dev", resolveAppEnv())
}

func TestResolveAppEnv_TrimsAndLowercases(t *testing.T) {
	t.Setenv("APP_ENV", "  PRODUCTION  ")
	assert.Equal(t, "production", resolveAppEnv())
}

func TestIsDevLikeAppEnv(t *testing.T) {
	for _, env := range []string{"local", "dev", "test"} {
		assert.True(t, isDevLikeAppEnv(env), "expected %q to be dev-like", env)
	}
	for _, env := range []string{"staging", "production", "prod", ""} {
		assert.False(t, isDevLikeAppEnv(env), "expected %q to not be dev-like", env)
	}
}

func TestLoggerEnv_DevLike_ReturnsDev(t *testing.T) {
	for _, env := range []string{"local", "dev", "test"} {
		assert.Equal(t, "dev", loggerEnv(env))
	}
}

func TestLoggerEnv_NonDev_ReturnsEnv(t *testing.T) {
	assert.Equal(t, "staging", loggerEnv("staging"))
	assert.Equal(t, "production", loggerEnv("production"))
}
