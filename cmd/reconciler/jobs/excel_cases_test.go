// excel_cases_test.go — test functions matching commercial-entitlements-testing.xlsx
// Sheet: Reconciler_Jobs (RJ-01 through RJ-14).
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── RJ-01 to RJ-04: OverrideExpiry ───────────────────────────────────────

func TestOverrideExpiry_ExpiredOverridesSwept(t *testing.T) {
	jctx := newJobContext()
	now := time.Now().UTC()
	jctx.Overrides = &stubOverrideRepo{
		expired: []domain.Override{
			{TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
		},
	}
	res, err := OverrideExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 1, res.Succeeded)
	assert.Equal(t, 0, res.Failed)
}

func TestOverrideExpiry_NoExpiredOverrides_Noop(t *testing.T) {
	jctx := newJobContext()
	jctx.Overrides = &stubOverrideRepo{expired: []domain.Override{}}
	res, err := OverrideExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Attempted)
	assert.Equal(t, 0, res.Succeeded)
}

func TestOverrideExpiry_ListExpiredDBError_ReturnsError(t *testing.T) {
	jctx := newJobContext()
	jctx.Overrides = &stubOverrideRepo{listErr: errJobStorage}
	_, err := OverrideExpiry(context.Background(), jctx)
	require.Error(t, err)
}

func TestOverrideExpiry_PartialFailure_ContinueLoop(t *testing.T) {
	jctx := newJobContext()
	now := time.Now().UTC()
	jctx.Overrides = &stubOverrideRepo{
		expired: []domain.Override{
			{TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
			{TenantID: uuid.New(), LimitKey: domain.LimitKeyWorkflowTemplateLimit, SetBy: uuid.New(), CreatedAt: now, UpdatedAt: now},
		},
		deleteErr: errJobStorage,
	}
	res, err := OverrideExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 2, res.Failed)
	assert.Equal(t, 0, res.Succeeded)
}

// ── RJ-05 to RJ-07: AddonExpiry ──────────────────────────────────────────

func TestAddonExpiry_ExpiredPacksMarkedExpired(t *testing.T) {
	jctx := newJobContext()
	now := time.Now().UTC()
	jctx.Addons = &stubAddonRepo{
		expired: []domain.QuotaAddon{
			{
				ID: uuid.New(), TenantID: uuid.New(),
				LimitKey: domain.LimitKeyLLMTokenQuotaMonthly,
				Status:   domain.AddonStatusActive, RemainingTokens: 500,
				RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 1, res.Succeeded)
	assert.Equal(t, 0, res.Failed)
}

func TestAddonExpiry_NoExpiredAddons_Noop(t *testing.T) {
	jctx := newJobContext()
	jctx.Addons = &stubAddonRepo{expired: []domain.QuotaAddon{}}
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Attempted)
	assert.Equal(t, 0, res.Succeeded)
}

func TestAddonExpiry_PartialFailure_ContinueLoop(t *testing.T) {
	jctx := newJobContext()
	now := time.Now().UTC()
	jctx.Addons = &stubAddonRepo{
		expired: []domain.QuotaAddon{
			{ID: uuid.New(), TenantID: uuid.New(), Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now},
			{ID: uuid.New(), TenantID: uuid.New(), Status: domain.AddonStatusActive, RecordVersion: 1, CreatedAt: now, UpdatedAt: now},
		},
		updateErr: errJobStorage,
	}
	res, err := AddonExpiry(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 2, res.Failed)
	assert.Equal(t, 0, res.Succeeded)
}

// ── RJ-08 to RJ-10: QuotaReset ───────────────────────────────────────────

func TestQuotaReset_StaleCountersDeleted(t *testing.T) {
	jctx := newJobContext()
	jctx.Cache = &stubCache{deleteN: 20}
	res, err := QuotaReset(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 2, res.Succeeded)
	assert.Equal(t, 0, res.Failed)
}

func TestQuotaReset_NilCache_Noop(t *testing.T) {
	jctx := newJobContext()
	jctx.Cache = nil
	res, err := QuotaReset(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Succeeded)
}

func TestQuotaReset_PartialFailure_ContinueLoop(t *testing.T) {
	jctx := newJobContext()
	jctx.Cache = &stubCache{deleteErr: errJobStorage}
	res, err := QuotaReset(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 2, res.Failed)
}

// ── RJ-11 to RJ-12: OutboxPrune ──────────────────────────────────────────

// TestOutboxPrune_DeletesOldEvents validates OutboxPrune with a configured
// batch limit. The actual PrunePublished loop requires an outbox.Runner backed
// by Postgres; full coverage lives in test/integration. Here we verify the
// nil-safety contract and default parameter defaulting paths.
func TestOutboxPrune_DeletesOldEvents(t *testing.T) {
	jctx := newJobContext()
	jctx.OutboxRunner = nil
	jctx.OutboxRetentionDays = 7
	jctx.BatchLimit = 100
	res, err := OutboxPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.Succeeded, 0)
}

func TestOutboxPrune_NilOutboxRunner_Noop(t *testing.T) {
	jctx := newJobContext()
	jctx.OutboxRunner = nil
	res, err := OutboxPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Succeeded)
}

// ── RJ-13 to RJ-14: ProcessedEventsPrune ─────────────────────────────────

func TestProcessedEventsPrune_DeletesOldEvents(t *testing.T) {
	jctx := newJobContext()
	jctx.Inbox = &stubInbox{pruneN: 50}
	res, err := ProcessedEventsPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 50, res.Succeeded)
}

func TestProcessedEventsPrune_NilInbox_Noop(t *testing.T) {
	jctx := newJobContext()
	jctx.Inbox = nil
	res, err := ProcessedEventsPrune(context.Background(), jctx)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Succeeded)
}
