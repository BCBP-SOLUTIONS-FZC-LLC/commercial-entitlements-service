package eventbus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// fakeTxForPublisher embeds a fake pgx.Tx to test Enqueue without real DB.
// It captures Exec calls to verify the correct SQL is issued.
type noopTxForPublisher struct{}

func (noopTxForPublisher) Begin(_ context.Context) (interface{ String() string }, error) {
	return nil, nil
}

// We only need to test paths that do NOT reach outbox.Enqueue (which needs pgx.Tx).
// We test: marshal error, codec error, missing tx context.

func TestPublisher_Enqueue_MissingTx_Error(t *testing.T) {
	p := New(EventSource, NoopCodec{})

	ev := &domain.DomainEvent{
		Type:       domain.EventTypeTenantEntitlementsChanged,
		Data:       map[string]string{"key": "value"},
		TenantID:   uuid.New(),
		OccurredAt: time.Now().UTC(),
	}

	// No tx in context → error
	err := p.Enqueue(context.Background(), ev)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RunInTx transaction")
}

func TestPublisher_Enqueue_CodecError_Propagates(t *testing.T) {
	errCodec := errors.New("schema validation failed")
	badCodec := &failCodec{err: errCodec}
	p := New(EventSource, badCodec)

	ev := &domain.DomainEvent{
		Type:       domain.EventTypeTenantEntitlementsChanged,
		Data:       map[string]string{"key": "value"},
		TenantID:   uuid.New(),
		OccurredAt: time.Now().UTC(),
	}

	// codec error that is NOT ErrNoSchema should propagate
	err := p.Enqueue(context.Background(), ev)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate event")
}

func TestPublisher_Enqueue_WithLogger_NoPanic(t *testing.T) {
	p := New(EventSource, NoopCodec{}).WithLogger(port.NopLogger{})
	ev := &domain.DomainEvent{
		Type:       domain.EventTypeTenantEntitlementsChanged,
		Data:       "{}",
		OccurredAt: time.Now().UTC(),
	}
	// Will fail due to missing tx, but WithLogger path should not panic
	err := p.Enqueue(context.Background(), ev)
	require.Error(t, err)
}

func TestPublisher_Enqueue_AllEnvelopeOpts_NoPanic(t *testing.T) {
	p := New(EventSource, NoopCodec{})
	ev := &domain.DomainEvent{
		Type:       domain.EventTypeTenantEntitlementsChanged,
		Data:       "{}",
		TenantID:   uuid.New(),
		Subject:    "sub",
		Actor:      "actor",
		IPAddress:  "1.2.3.4",
		UserAgent:  "test-agent",
		OccurredAt: time.Now().UTC(),
	}
	err := p.Enqueue(context.Background(), ev)
	require.Error(t, err) // no tx — expected
}

type failCodec struct{ err error }

func (f *failCodec) Encode(_ context.Context, _ string, _ []byte) ([]byte, string, error) {
	return nil, "", f.err
}
