package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	platformInbox "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/inbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// IdempotencyRepository provides at-most-once delivery semantics for one
// SQS consumer by wrapping platform-events inbox.Store. The consumer name
// is fixed at construction time (IDEMP-1..4, EVT-10).
//
// ProcessOnce injects the inbox transaction into context via port.WithTx so
// that TxRunner.RunInTx (join-mode) reuses it: the processed_events claim,
// the snapshot upsert, and the outbox.Enqueue all commit atomically in one
// transaction.
type IdempotencyRepository struct {
	store     *platformInbox.Store
	publisher port.EventPublisher
	audit     port.AuditRecorder
}

// NewIdempotencyRepository returns an IdempotencyRepository for the named
// consumer. consumer must be a stable, non-empty string (e.g. the queue name).
func NewIdempotencyRepository(pool *pgcommon.Pool, publisher port.EventPublisher, consumer string) (*IdempotencyRepository, error) {
	store, err := platformInbox.NewStore(pool, consumer)
	if err != nil {
		return nil, err
	}
	return &IdempotencyRepository{store: store, publisher: publisher}, nil
}

// WithAuditRecorder injects an audit recorder for context propagation inside
// ProcessOnce. When TxRunner.RunInTx operates in join-mode the recorder is
// also injected there, so setting it here is only needed for paths that bypass
// TxRunner.
func (r *IdempotencyRepository) WithAuditRecorder(audit port.AuditRecorder) *IdempotencyRepository {
	r.audit = audit
	return r
}

var _ port.Inbox = (*IdempotencyRepository)(nil)

// ProcessOnce calls fn exactly once per eventID for this consumer.
// It delegates to platform-events inbox.Store.Process which: claims the event
// in processed_events within a transaction, calls fn with that transaction,
// and commits both atomically. A duplicate silently skips fn.
// Returns duplicate=true when the event was already processed.
func (r *IdempotencyRepository) ProcessOnce(ctx context.Context, eventID, eventType string, fn func(txCtx context.Context) error) (bool, error) {
	env := events.Envelope[json.RawMessage]{ID: eventID, Type: eventType}
	fnCalled := false
	err := r.store.Process(ctx, env, func(ctx context.Context, tx pgcommon.Tx) error {
		fnCalled = true
		// Inject the inbox transaction and event infrastructure into context.
		// TxRunner.RunInTx detects the tx and joins this transaction instead
		// of opening a new one, keeping the dedup claim and the business write
		// (snapshot upsert + outbox.Enqueue) in one atomic transaction.
		txCtx := port.WithTx(ctx, tx)
		if r.publisher != nil {
			txCtx = port.WithEventPublisher(txCtx, r.publisher)
		}
		if r.audit != nil {
			txCtx = port.WithAuditRecorder(txCtx, r.audit)
		}
		return fn(txCtx)
	})
	duplicate := !fnCalled && err == nil
	return duplicate, err
}

// Prune deletes this consumer's processed_events rows older than ttl via
// platform-events inbox.Store.Prune.
func (r *IdempotencyRepository) Prune(ctx context.Context, ttl time.Duration, batchLimit int) (int64, error) {
	return r.store.Prune(ctx, ttl, batchLimit)
}

// ── MultiPruneInbox ────────────────────────────────────────────────────────

// MultiPruneInbox aggregates several per-consumer IdempotencyRepository
// instances so the reconciler's processed-events-prune job can prune all
// consumers in one call. ProcessOnce is not supported and returns an error.
type MultiPruneInbox struct {
	repos []*IdempotencyRepository
}

// NewMultiPruneInbox builds a MultiPruneInbox from the given consumer repos.
func NewMultiPruneInbox(repos ...*IdempotencyRepository) *MultiPruneInbox {
	return &MultiPruneInbox{repos: repos}
}

var _ port.IdempotencyRepository = (*MultiPruneInbox)(nil)

// ProcessOnce is not implemented on MultiPruneInbox; use a per-consumer
// IdempotencyRepository from the server wiring.
func (m *MultiPruneInbox) ProcessOnce(_ context.Context, _, _ string, _ func(context.Context) error) (bool, error) {
	return false, errors.New("MultiPruneInbox: ProcessOnce not supported — use per-consumer inbox")
}

// Prune runs Prune on each consumer's store sequentially and returns the
// aggregate row count.
func (m *MultiPruneInbox) Prune(ctx context.Context, ttl time.Duration, batchLimit int) (int64, error) {
	var total int64
	for _, r := range m.repos {
		n, err := r.Prune(ctx, ttl, batchLimit)
		total += n
		if err != nil {
			return total, err
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
	return total, nil
}
