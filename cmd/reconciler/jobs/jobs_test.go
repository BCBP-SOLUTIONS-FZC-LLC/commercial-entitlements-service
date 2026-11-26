// Package jobs — white-box unit tests for all reconciler job functions.
// No Docker required; all infrastructure is stubbed.
package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

var errJobStorage = errors.New("storage failure")

// ── stubs ──────────────────────────────────────────────────────────────────

type stubAddonRepo struct {
	expired   []domain.QuotaAddon
	updateErr error
	listErr   error
}

func (r *stubAddonRepo) ListExpired(_ context.Context, _ time.Time) ([]domain.QuotaAddon, error) {
	return r.expired, r.listErr
}
func (r *stubAddonRepo) Update(_ context.Context, a *domain.QuotaAddon) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	return nil
}
func (r *stubAddonRepo) Insert(_ context.Context, _ *domain.QuotaAddon) error { return nil }
func (r *stubAddonRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.QuotaAddon, error) {
	return nil, nil
}
func (r *stubAddonRepo) GetByBillingRef(_ context.Context, _ uuid.UUID, _ string) (*domain.QuotaAddon, error) {
	return nil, nil
}
func (r *stubAddonRepo) ListByTenant(_ context.Context, _ uuid.UUID, _ *domain.LimitKey) ([]domain.QuotaAddon, error) {
	return nil, nil
}
func (r *stubAddonRepo) ListActiveByTenantAndKey(_ context.Context, _ uuid.UUID, _ domain.LimitKey) ([]domain.QuotaAddon, error) {
	return nil, nil
}
func (r *stubAddonRepo) SumRemainingByTenantAndKey(_ context.Context, _ uuid.UUID, _ domain.LimitKey) (int64, error) {
	return 0, nil
}

type stubOverrideRepo struct {
	expired   []domain.Override
	deleteErr error
	listErr   error
}

func (r *stubOverrideRepo) ListExpired(_ context.Context, _ time.Time) ([]domain.Override, error) {
	return r.expired, r.listErr
}
func (r *stubOverrideRepo) Delete(_ context.Context, _ uuid.UUID, _ domain.LimitKey) error {
	return r.deleteErr
}
func (r *stubOverrideRepo) InsertHistory(_ context.Context, _ *domain.OverrideHistory) error {
	return nil
}
func (r *stubOverrideRepo) Upsert(_ context.Context, _ *domain.Override) error { return nil }
func (r *stubOverrideRepo) Get(_ context.Context, _ uuid.UUID, _ domain.LimitKey) (*domain.Override, error) {
	return nil, domain.ErrOverrideNotFound
}
func (r *stubOverrideRepo) ListByTenant(_ context.Context, _ uuid.UUID) ([]domain.Override, error) {
	return nil, nil
}
func (r *stubOverrideRepo) ListHistory(_ context.Context, _ uuid.UUID, _ domain.LimitKey) ([]domain.OverrideHistory, error) {
	return nil, nil
}

type stubEffectSvc struct {
	recomputeErr error
}

func (s *stubEffectSvc) GetEffectiveSet(_ context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error) {
	return &domain.EffectiveSet{TenantID: tenantID}, nil
}
func (s *stubEffectSvc) Recompute(_ context.Context, tenantID uuid.UUID, _ domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
	return &domain.EffectiveSet{TenantID: tenantID}, s.recomputeErr
}

type stubTxRunner struct{ err error }

func (r *stubTxRunner) RunInTx(_ context.Context, fn func(context.Context) error) error {
	if r.err != nil {
		return r.err
	}
	return fn(context.Background())
}

type stubInbox struct {
	pruneN   int64
	pruneErr error
}

func (i *stubInbox) ProcessOnce(_ context.Context, _, _ string, fn func(context.Context) error) (bool, error) {
	return false, fn(context.Background())
}
func (i *stubInbox) Prune(_ context.Context, _ time.Duration, _ int) (int64, error) {
	return i.pruneN, i.pruneErr
}

type stubCache struct {
	deleteN   int64
	deleteErr error
}

func (c *stubCache) GetEffectiveSet(_ context.Context, _ uuid.UUID) (*domain.EffectiveSet, error) {
	return nil, nil
}
func (c *stubCache) SetEffectiveSet(_ context.Context, _ uuid.UUID, _ *domain.EffectiveSet) error {
	return nil
}
func (c *stubCache) InvalidateEffectiveSet(_ context.Context, _ uuid.UUID) error { return nil }
func (c *stubCache) SetQuotaCeiling(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ *int64) error {
	return nil
}
func (c *stubCache) ResetUsageCounter(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ string) error {
	return nil
}
func (c *stubCache) DeleteUsageCounters(_ context.Context, _ domain.LimitKey, _ string) (int64, error) {
	return c.deleteN, c.deleteErr
}
func (c *stubCache) GetPlan(_ context.Context, _ string) (*domain.Plan, error) { return nil, nil }
func (c *stubCache) SetPlan(_ context.Context, _ *domain.Plan) error           { return nil }
func (c *stubCache) InvalidatePlan(_ context.Context, _ string) error          { return nil }
func (c *stubCache) Health(_ context.Context) error                            { return nil }
func (c *stubCache) Close() error                                              { return nil }

func newJobContext() *Context {
	return &Context{
		TxRunner:  &stubTxRunner{},
		Addons:    &stubAddonRepo{},
		Overrides: &stubOverrideRepo{},
		EffectSvc: &stubEffectSvc{},
		Logger:    port.NewSlogStyleLogger(port.NopLogger{}),
	}
}

// ── AddonExpiry ───────────────────────────────────────────────────────────

func TestAddonExpiry_NoExpired(t *testing.T) {
	jctx := newJobContext()
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Attempted)
	assert.Equal(t, 0, res.Succeeded)
}

func TestAddonExpiry_ExpiresOne(t *testing.T) {
	jctx := newJobContext()
	now := time.Now().UTC()
	jctx.Addons.(*stubAddonRepo).expired = []domain.QuotaAddon{
		{ID: uuid.New(), TenantID: uuid.New(), LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
			Status: domain.AddonStatusActive, RemainingTokens: 300, RecordVersion: 1,
			CreatedAt: now, UpdatedAt: now},
	}
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 1, res.Succeeded)
}

func TestAddonExpiry_UpdateError_Counted(t *testing.T) {
	jctx := newJobContext()
	now := time.Now().UTC()
	jctx.Addons.(*stubAddonRepo).expired = []domain.QuotaAddon{
		{ID: uuid.New(), TenantID: uuid.New(), Status: domain.AddonStatusActive,
			RecordVersion: 1, CreatedAt: now, UpdatedAt: now},
	}
	jctx.Addons.(*stubAddonRepo).updateErr = errJobStorage
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 0, res.Succeeded)
}

func TestAddonExpiry_NilEffectSvc_NoRecompute(t *testing.T) {
	jctx := newJobContext()
	jctx.EffectSvc = nil
	now := time.Now().UTC()
	jctx.Addons.(*stubAddonRepo).expired = []domain.QuotaAddon{
		{ID: uuid.New(), TenantID: uuid.New(), Status: domain.AddonStatusActive,
			RecordVersion: 1, CreatedAt: now, UpdatedAt: now},
	}
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Succeeded)
}

func TestAddonExpiry_RecomputeError_NonFatal(t *testing.T) {
	jctx := newJobContext()
	jctx.EffectSvc = &stubEffectSvc{recomputeErr: errJobStorage}
	now := time.Now().UTC()
	jctx.Addons.(*stubAddonRepo).expired = []domain.QuotaAddon{
		{ID: uuid.New(), TenantID: uuid.New(), Status: domain.AddonStatusActive,
			RecordVersion: 1, CreatedAt: now, UpdatedAt: now},
	}
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Succeeded)
}

func TestAddonExpiry_ListError(t *testing.T) {
	jctx := newJobContext()
	jctx.Addons.(*stubAddonRepo).listErr = errJobStorage
	_, err := AddonExpiry(context.Background(), jctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list expired")
}

// ── OverrideExpiry ────────────────────────────────────────────────────────

func TestOverrideExpiry_NoExpired(t *testing.T) {
	jctx := newJobContext()
	res, err := OverrideExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Attempted)
}

func TestOverrideExpiry_ExpiresOne(t *testing.T) {
	jctx := newJobContext()
	past := time.Now().UTC().Add(-time.Hour)
	jctx.Overrides.(*stubOverrideRepo).expired = []domain.Override{
		{TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
			Value: nil, Reason: "expired", SetBy: uuid.New(), ExpiresAt: &past},
	}
	res, err := OverrideExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Succeeded)
}

func TestOverrideExpiry_DeleteError_Logged(t *testing.T) {
	jctx := newJobContext()
	past := time.Now().UTC().Add(-time.Hour)
	jctx.Overrides.(*stubOverrideRepo).expired = []domain.Override{
		{TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
			Reason: "expired", SetBy: uuid.New(), ExpiresAt: &past},
	}
	jctx.Overrides.(*stubOverrideRepo).deleteErr = errJobStorage
	res, err := OverrideExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
}

func TestOverrideExpiry_ListError(t *testing.T) {
	jctx := newJobContext()
	jctx.Overrides.(*stubOverrideRepo).listErr = errJobStorage
	_, err := OverrideExpiry(context.Background(), jctx)
	require.Error(t, err)
}

func TestOverrideExpiry_NilEffectSvc_NoRecompute(t *testing.T) {
	jctx := newJobContext()
	jctx.EffectSvc = nil
	past := time.Now().UTC().Add(-time.Hour)
	jctx.Overrides.(*stubOverrideRepo).expired = []domain.Override{
		{TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
			Reason: "expired", SetBy: uuid.New(), ExpiresAt: &past},
	}
	res, err := OverrideExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Succeeded)
}

// ── OutboxPrune ───────────────────────────────────────────────────────────

func TestOutboxPrune_NilRunner_SkipsWithWarn(t *testing.T) {
	jctx := newJobContext()
	jctx.OutboxRunner = nil
	res, err := OutboxPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Succeeded)
}

// ── ProcessedEventsPrune ──────────────────────────────────────────────────

func TestProcessedEventsPrune_NilInbox_SkipsWithWarn(t *testing.T) {
	jctx := newJobContext()
	jctx.Inbox = nil
	res, err := ProcessedEventsPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Succeeded)
}

func TestProcessedEventsPrune_PrunesRows(t *testing.T) {
	jctx := newJobContext()
	inbox := &stubInbox{pruneN: 50}
	jctx.Inbox = inbox
	res, err := ProcessedEventsPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 50, res.Succeeded)
}

func TestProcessedEventsPrune_PruneError_Propagates(t *testing.T) {
	jctx := newJobContext()
	jctx.Inbox = &stubInbox{pruneErr: errJobStorage}
	_, err := ProcessedEventsPrune(context.Background(), jctx)
	require.Error(t, err)
}

func TestProcessedEventsPrune_DefaultTTL(t *testing.T) {
	jctx := newJobContext()
	jctx.ProcessedEventsTTLDays = 0 // should default to 8
	jctx.Inbox = &stubInbox{pruneN: 0}
	res, err := ProcessedEventsPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Succeeded)
}

// ── QuotaReset ────────────────────────────────────────────────────────────

func TestQuotaReset_NilCache_SkipsWithWarn(t *testing.T) {
	jctx := newJobContext()
	jctx.Cache = nil
	res, err := QuotaReset(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Succeeded)
}

func TestQuotaReset_DeletesUsageCounters(t *testing.T) {
	jctx := newJobContext()
	cache := &stubCache{deleteN: 10}
	jctx.Cache = cache
	res, err := QuotaReset(context.Background(), jctx)
	require.NoError(t, err)
	// 2 limit keys × success
	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 2, res.Succeeded)
}

func TestQuotaReset_DeleteError_Counted(t *testing.T) {
	jctx := newJobContext()
	cache := &stubCache{deleteErr: errJobStorage}
	jctx.Cache = cache
	res, err := QuotaReset(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 2, res.Failed)
}
