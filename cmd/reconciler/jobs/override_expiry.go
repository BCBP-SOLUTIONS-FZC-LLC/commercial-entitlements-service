package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// OverrideExpiry sweeps entitlement overrides whose expires_at has passed,
// records history entries, and triggers a recompute for each affected tenant.
func OverrideExpiry(ctx context.Context, jctx *Context) (Result, error) {
	var res Result
	now := time.Now().UTC()
	expired, err := jctx.Overrides.ListExpired(ctx, now)
	if err != nil {
		return res, fmt.Errorf("override-expiry: list expired: %w", err)
	}
	res.Attempted = len(expired)
	for i := range expired {
		ov := &expired[i]
		hist := &domain.OverrideHistory{
			TenantID:  ov.TenantID,
			LimitKey:  ov.LimitKey,
			OldValue:  ov.Value,
			NewValue:  nil,
			Action:    domain.ActionExpired,
			Reason:    "override expired (reconciler)",
			ActorID:   ov.SetBy,
			CreatedAt: now,
		}
		txErr := jctx.TxRunner.RunInTx(ctx, func(txCtx context.Context) error {
			if err := jctx.Overrides.Delete(txCtx, ov.TenantID, ov.LimitKey); err != nil {
				return err
			}
			return jctx.Overrides.InsertHistory(txCtx, hist)
		})
		if txErr != nil {
			res.Failed++
			jctx.Logger.Warn("override-expiry: failed to expire override",
				"tenant_id", ov.TenantID, "key", ov.LimitKey, "error", txErr.Error())
			continue
		}
		if jctx.EffectSvc != nil {
			if _, err := jctx.EffectSvc.Recompute(ctx, ov.TenantID, domain.CauseOverrideChange); err != nil {
				jctx.Logger.Warn("override-expiry: recompute failed",
					"tenant_id", ov.TenantID, "error", err.Error())
			}
		}
		res.Succeeded++
	}
	jctx.Logger.Info("override-expiry complete",
		"attempted", res.Attempted, "succeeded", res.Succeeded, "failed", res.Failed)
	return res, nil
}
