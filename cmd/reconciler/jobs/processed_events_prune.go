package jobs

import (
	"context"
	"fmt"
	"time"
)

// ProcessedEventsPrune deletes processed_events rows older than
// the TTL to prevent unbounded table growth.
func ProcessedEventsPrune(ctx context.Context, jctx *Context) (Result, error) {
	var res Result
	if jctx.Inbox == nil {
		jctx.Logger.Warn("processed-events-prune: no inbox wired — skipping")
		return res, nil
	}
	ttlDays := jctx.ProcessedEventsTTLDays
	if ttlDays <= 0 {
		ttlDays = 8
	}
	batchLimit := jctx.BatchLimit
	if batchLimit <= 0 {
		batchLimit = defaultPruneBatchLimit
	}
	ttl := time.Duration(ttlDays) * 24 * time.Hour
	for {
		n, err := jctx.Inbox.Prune(ctx, ttl, batchLimit)
		res.Attempted += int(n)
		res.Succeeded += int(n)
		if err != nil {
			return res, fmt.Errorf("processed-events-prune: %w", err)
		}
		if n < int64(batchLimit) {
			break
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
	}
	jctx.Logger.Info("processed-events-prune complete", "deleted", res.Succeeded, "ttl_days", ttlDays)
	return res, nil
}
