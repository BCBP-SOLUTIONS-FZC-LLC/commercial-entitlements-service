package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// AddonExpiry marks quota addon packs whose expires_at has passed as
// expired, zeroes their remaining_tokens, and triggers recompute.
func AddonExpiry(ctx context.Context, jctx *Context) (Result, error) {
	var res Result
	now := time.Now().UTC()
	expired, err := jctx.Addons.ListExpired(ctx, now)
	if err != nil {
		return res, fmt.Errorf("addon-expiry: list expired: %w", err)
	}
	res.Attempted = len(expired)
	for i := range expired {
		a := &expired[i]
		a.Status = domain.AddonStatusExpired
		a.RemainingTokens = 0
		a.RecordVersion++
		a.UpdatedAt = now

		txErr := jctx.TxRunner.RunInTx(ctx, func(txCtx context.Context) error {
			return jctx.Addons.Update(txCtx, a)
		})
		if txErr != nil {
			res.Failed++
			jctx.Logger.Warn("addon-expiry: failed to expire addon",
				"addon_id", a.ID, "tenant_id", a.TenantID, "error", txErr.Error())
			continue
		}
		if jctx.EffectSvc != nil {
			if _, err := jctx.EffectSvc.Recompute(ctx, a.TenantID, domain.CauseAddonChange); err != nil {
				jctx.Logger.Warn("addon-expiry: recompute failed",
					"tenant_id", a.TenantID, "error", err.Error())
			}
		}
		metrics.IncAddonExpire()
		res.Succeeded++
	}
	jctx.Logger.Info("addon-expiry complete",
		"attempted", res.Attempted, "succeeded", res.Succeeded, "failed", res.Failed)
	return res, nil
}
