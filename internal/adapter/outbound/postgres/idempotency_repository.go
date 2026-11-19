package postgres

import (
	"context"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

// IdempotencyRepository provides at-most-once delivery semantics for event
// consumers by tracking processed event IDs in processed_events.
// It implements both port.Inbox (for consumers) and port.IdempotencyRepository
// (for the reconciler's prune job).
type IdempotencyRepository struct {
	pool      *pgcommon.Pool
	publisher port.EventPublisher
}

// NewIdempotencyRepository returns a new IdempotencyRepository.
func NewIdempotencyRepository(pool *pgcommon.Pool, publisher port.EventPublisher) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool, publisher: publisher}
}

// Ensure it implements port.Inbox and port.IdempotencyRepository.
var _ port.Inbox = (*IdempotencyRepository)(nil)

// ProcessOnce calls fn inside a transaction only if (consumer, eventID)
// has not been processed before. Claims the event in processed_events and
// calls fn in the same transaction (IDEMP-1..4). Returns duplicate=true when
// the event was already processed.
func (r *IdempotencyRepository) ProcessOnce(ctx context.Context, consumer, eventID, eventType string, fn func(txCtx context.Context) error) (duplicate bool, err error) {
	txRunner := NewTxRunner(r.pool, r.publisher)
	err = txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		tx, _ := port.TxFromContext(txCtx)
		tag, insertErr := tx.Exec(txCtx, `
			INSERT INTO processed_events (consumer, event_id, event_type, processed_at)
			VALUES ($1,$2,$3,now())
			ON CONFLICT (consumer, event_id) DO NOTHING`,
			consumer, eventID, eventType)
		if insertErr != nil {
			return wrapConnErr(insertErr)
		}
		if tag.RowsAffected() == 0 {
			duplicate = true
			return nil // already processed; skip fn
		}
		return fn(txCtx)
	})
	return duplicate, err
}

// Prune deletes processed_events rows older than the given duration.
// Returns the number of rows deleted.
func (r *IdempotencyRepository) Prune(ctx context.Context, olderThan time.Duration, batchLimit int) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	var deleted int64
	err := withPool(ctx, r.pool, func(tx pgcommon.Tx) error {
		// PostgreSQL does not support LIMIT on DELETE directly; use a CTE.
		tag, err := tx.Exec(ctx, `
			DELETE FROM processed_events
			WHERE ctid IN (
			  SELECT ctid FROM processed_events
			  WHERE processed_at < $1
			  LIMIT $2
			)`, cutoff, batchLimit)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected()
		return nil
	})
	return deleted, err
}
