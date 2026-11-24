package jobs

import (
	"context"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// QuotaReset deletes stale per-period usage counters in Valkey at billing
// period rollover (ENT-D13). Uses SCAN+DEL on the pattern
// quota:usage:*:{limitKey}:{prevPeriod} to clear all tenants in one pass
// without enumerating tenant IDs from Postgres.
func QuotaReset(ctx context.Context, jctx *Context) (Result, error) {
	var res Result
	if jctx.Cache == nil {
		jctx.Logger.Warn("quota-reset: no cache wired — skipping")
		return res, nil
	}

	now := time.Now().UTC()
	period := now.Format("2006-01")
	prevPeriod := now.AddDate(0, -1, 0).Format("2006-01")

	jctx.Logger.Info("quota-reset: clearing stale usage counters",
		"active_period", period, "clearing_period", prevPeriod)

	periodicKeys := []domain.LimitKey{
		domain.LimitKeyLLMTokenQuotaMonthly,
		domain.LimitKeyAPIRequestBudgetMonthly,
	}

	for _, key := range periodicKeys {
		res.Attempted++
		deleted, err := jctx.Cache.DeleteUsageCounters(ctx, key, prevPeriod)
		if err != nil {
			jctx.Logger.Warn("quota-reset: delete usage counters failed",
				"limit_key", string(key), "period", prevPeriod, "error", err.Error())
			res.Failed++
			continue
		}
		jctx.Logger.Info("quota-reset: cleared usage counters",
			"limit_key", string(key), "period", prevPeriod, "deleted_keys", deleted)
		res.Succeeded++
	}

	jctx.Logger.Info("quota-reset complete",
		"period", prevPeriod, "attempted", res.Attempted, "succeeded", res.Succeeded, "failed", res.Failed)
	return res, nil
}
