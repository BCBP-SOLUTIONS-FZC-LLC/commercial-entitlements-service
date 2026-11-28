//go:build integration

// E-9 — TenantMembershipsPurged forfeits the tenant's add-on balance (CD-55;
// HLD v0.69 P-5a for an unconverted trial). Packs move to status 'forfeited'
// (migration 000007); rows stay because quota_addon_drawdowns references them
// ON DELETE RESTRICT and is a 7-year financial record.
package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

func TestE9_Purge_ForfeitsPacks_KeepsDrawdowns(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)

	drawn, _ := e.grantPack(t, ctx, tenantID, 30_000) // 20,000 of 50,000 drawn
	untouched, _ := e.grantPack(t, ctx, tenantID, 50_000)
	require.NoError(t, e.seed.Exec(context.Background(), `
		INSERT INTO quota_addon_drawdowns (addon_id, tenant_id, limit_key, tokens_consumed, period_start)
		VALUES ($1, $2, 'llm_token_quota_monthly', 20000, $3)`,
		drawn, tenantID, time.Now().UTC().AddDate(0, -1, 0)))

	purge := func(eventID string) (int, error) {
		var n int
		_, err := e.inbox.ProcessOnce(ctx, eventID, "TenantMembershipsPurged", func(txCtx context.Context) error {
			var ferr error
			n, ferr = e.svc.ForfeitAddonsAtPurge(txCtx, tenantID)
			return ferr
		})
		return n, err
	}

	n, err := purge(uuid.NewString())
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	for _, id := range []uuid.UUID{drawn, untouched} {
		a, err := e.addons.GetByID(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, domain.AddonStatusForfeited, a.Status)
		assert.Zero(t, a.RemainingTokens)
		assert.Equal(t, int64(50_000), a.TotalTokens)
	}

	var drawdowns int
	require.NoError(t, e.seed.RunQueryRow(context.Background(), func(r pgcommon.Row) error { return r.Scan(&drawdowns) },
		`SELECT count(*) FROM quota_addon_drawdowns WHERE tenant_id = $1`, tenantID))
	assert.Equal(t, 1, drawdowns, "drawdown ledger is kept")

	sum, err := e.addons.SumRemainingByTenantAndKey(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, err)
	assert.Zero(t, sum)

	// A redelivery under a new envelope ID forfeits nothing more.
	n, err = purge(uuid.NewString())
	require.NoError(t, err)
	assert.Zero(t, n)
}
