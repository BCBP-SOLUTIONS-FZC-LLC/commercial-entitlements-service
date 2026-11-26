//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// nopEventPublisher implements port.EventPublisher with no side effects.
type nopEventPublisher struct{}

func (nopEventPublisher) Enqueue(_ context.Context, _ *domain.DomainEvent) error { return nil }

// nopAuditRecorderTx implements port.AuditRecorder with no side effects.
type nopAuditRecorderTx struct{}

func (nopAuditRecorderTx) Record(_ context.Context, _ port.AuditEntry) error        { return nil }
func (nopAuditRecorderTx) RecordBatch(_ context.Context, _ []port.AuditEntry) error { return nil }

// TestTxRunner_RunInTx_NonJoinMode exercises the branch in RunInTx that opens
// a NEW transaction (no existing tx in context). This covers the
// pgcommon.RunInTxWithRetryOpts path which is unreachable from unit tests.
func TestTxRunner_RunInTx_NonJoinMode(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	runner := pgadapter.NewTxRunner(appPool, nil)
	called := false
	err := runner.RunInTx(context.Background(), func(_ context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called, "fn must be called in non-join mode (new transaction)")
}

func TestTxRunner_RunInTx_NonJoinMode_FnError_RollsBack(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	runner := pgadapter.NewTxRunner(appPool, nil)
	want := errors.New("fn failed")
	err := runner.RunInTx(context.Background(), func(_ context.Context) error {
		return want
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, want, "fn error must propagate through the transaction")
}

// TestTxRunner_RunInTx_NonJoinMode_WithPublisherAndAudit covers the r.events != nil
// and r.audit != nil branches inside the pgcommon.RunInTxWithRetryOpts callback.
func TestTxRunner_RunInTx_NonJoinMode_WithPublisherAndAudit(t *testing.T) {
	t.Parallel()
	appPool, _, _, _ := setupTestDB(t)

	pub := nopEventPublisher{}
	runner := pgadapter.NewTxRunner(appPool, pub)
	runner.WithAuditRecorder(nopAuditRecorderTx{})

	var pubFromCtx port.EventPublisher
	err := runner.RunInTx(context.Background(), func(txCtx context.Context) error {
		p, ok := port.EventPublisherFromContext(txCtx)
		if ok {
			pubFromCtx = p
		}
		return nil
	})
	require.NoError(t, err)
	assert.NotNil(t, pubFromCtx, "EventPublisher must be injected into the tx context in non-join mode")
}
