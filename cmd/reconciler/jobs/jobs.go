// Package jobs contains the reconciler job implementations for the
// commercial-entitlements service. Each job is a pure function dispatched by
// cmd/reconciler/main.go.
package jobs

import (
	"context"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/outbox"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// Func is the signature for all reconciler job functions.
type Func func(ctx context.Context, jctx *Context) (Result, error)

// Result summarises the outcome of a reconciler run.
type Result struct {
	Attempted int
	Succeeded int
	Failed    int
	Skipped   int
}

// PruneableInbox can prune processed_events rows.
type PruneableInbox interface {
	port.Inbox
	Prune(ctx context.Context, olderThan time.Duration, batchLimit int) (int64, error)
}

// Context carries all infrastructure dependencies for a reconciler job run.
type Context struct {
	TxRunner   port.TxRunner
	Overrides  port.OverrideRepository
	Addons     port.AddonRepository
	Shortfalls port.ShortfallRepository
	EffectSvc  port.EffectiveSetService
	TrialSvc   port.TrialService
	Cache      port.Cache
	Inbox      PruneableInbox

	OutboxRunner *outbox.Runner
	Logger       port.SlogStyleLogger
	Metrics      interface{}

	BatchLimit             int
	OutboxRetentionDays    int
	ProcessedEventsTTLDays int
	InboxConsumers         []string
}
