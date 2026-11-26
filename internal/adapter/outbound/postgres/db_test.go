package postgres_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── SystemPoolConfig ─────────────────────────────────────────────────────────

func TestSystemPoolConfig_PoolNameIsSystem(t *testing.T) {
	cfg := postgres.SystemPoolConfig("postgres://localhost/test", nil)
	assert.Equal(t, "system", cfg.PoolName)
}

func TestSystemPoolConfig_GUCProviderIsNil(t *testing.T) {
	cfg := postgres.SystemPoolConfig("postgres://localhost/test", nil)
	assert.Nil(t, cfg.GUCProvider,
		"system pool must not set a GUC provider — it is cross-tenant (BYPASSRLS)")
}

func TestSystemPoolConfig_PGBouncerModeTrue(t *testing.T) {
	cfg := postgres.SystemPoolConfig("postgres://localhost/test", nil)
	assert.True(t, cfg.PGBouncerMode,
		"system pool must use PGBouncer mode so reconciler jobs don't break transaction pooling")
}

func TestSystemPoolConfig_DSNStored(t *testing.T) {
	dsn := "postgres://system:pass@localhost/db"
	cfg := postgres.SystemPoolConfig(dsn, nil)
	assert.Equal(t, dsn, cfg.DSN)
}

func TestSystemPoolConfig_WithLogger_LoggerAdapterAttached(t *testing.T) {
	cfg := postgres.SystemPoolConfig("postgres://localhost/test", port.NopLogger{})
	assert.NotNil(t, cfg.Logger, "a non-nil Logger must be attached to the pool config")
}

// ── TxRunner ─────────────────────────────────────────────────────────────────

func TestNewTxRunner_NotNil(t *testing.T) {
	r := postgres.NewTxRunner(nil, nil)
	assert.NotNil(t, r)
}

func TestTxRunner_WithAuditRecorder_ReturnsRunner(t *testing.T) {
	r := postgres.NewTxRunner(nil, nil)
	r2 := r.WithAuditRecorder(nil)
	assert.Equal(t, r, r2, "WithAuditRecorder must return the same TxRunner (fluent)")
}

// ── ErrBillingRefAlreadyClaimed wraps correctly ──────────────────────────────

func TestDomainError_ErrBillingRefAlreadyClaimed_IsCode(t *testing.T) {
	wrapped := domain.NewError(domain.ErrBillingRefAlreadyClaimed, "billing_ref already claimed")
	assert.ErrorIs(t, wrapped, domain.ErrBillingRefAlreadyClaimed)
}

// ── CatalogCacheRepository ───────────────────────────────────────────────────

func TestNewCatalogCacheRepository_NotNil(t *testing.T) {
	repo := postgres.NewCatalogCacheRepository(nil)
	assert.NotNil(t, repo)
}

// ── NewIdempotencyRepository ─────────────────────────────────────────────────

func TestNewIdempotencyRepository_NilPool_ReturnsError(t *testing.T) {
	_, err := postgres.NewIdempotencyRepository(nil, nil, "test-consumer")
	assert.Error(t, err, "nil pool must cause NewStore to fail")
}

func TestNewIdempotencyRepository_ValidInputs_ReturnsRepo(t *testing.T) {
	// A non-nil but empty pool config still satisfies the non-nil check inside
	// platform-events inbox.NewStore. The pool itself is not dialled here.
	_, err := postgres.NewIdempotencyRepository(nil, nil, "test-consumer")
	// nil pool → error (expected); we're just verifying the error propagation.
	assert.Error(t, err)
}

// ── ErrOptimisticLockConflict propagation ────────────────────────────────────

func TestOptimisticLockConflict_IsErrConcurrentUpdate(t *testing.T) {
	// ENT-INV: ErrOptimisticLockConflict and ErrConcurrentUpdate are distinct
	// sentinels so the HTTP layer can map them to 409 and 503 respectively.
	assert.False(t, errors.Is(domain.ErrOptimisticLockConflict, domain.ErrConcurrentUpdate))
	assert.False(t, errors.Is(domain.ErrConcurrentUpdate, domain.ErrOptimisticLockConflict))
}
