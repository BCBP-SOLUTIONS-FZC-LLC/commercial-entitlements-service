package jobs

import (
	"context"
	"time"
)

const defaultPruneBatchLimit = 500

// OutboxPrune deletes published outbox_events rows older than the
// retention window via platform-events' outbox.Runner.PrunePublished.
// Retention defaults to 8 days (OUTBOX_RETENTION_DAYS). Called in a loop
// until PrunePublished returns fewer rows than batchLimit.
func OutboxPrune(ctx context.Context, jctx *Context) (Result, error) {
	var res Result
	if jctx.OutboxRunner == nil {
		jctx.Logger.Warn("outbox-prune: no outbox.Runner wired — skipping")
		return res, nil
	}
	batchLimit := jctx.BatchLimit
	if batchLimit <= 0 {
		batchLimit = defaultPruneBatchLimit
	}
	retentionDays := jctx.OutboxRetentionDays
	if retentionDays <= 0 {
		retentionDays = 8
	}
	retention := time.Duration(retentionDays) * 24 * time.Hour

	for {
		n, err := jctx.OutboxRunner.PrunePublished(ctx, retention, batchLimit)
		res.Attempted += int(n)
		res.Succeeded += int(n)
		if err != nil {
			return res, err
		}
		if n < int64(batchLimit) {
			break
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
	}
	jctx.Logger.Info("outbox-prune complete", "deleted", res.Succeeded, "retention_days", retentionDays)
	return res, nil
}
