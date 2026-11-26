package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEnv(id, eventType string) events.Envelope[json.RawMessage] {
	return events.Envelope[json.RawMessage]{ID: id, Type: eventType, Payload: json.RawMessage(`{}`)}
}

// ── sanitizeEventType ─────────────────────────────────────────────────────

func TestSanitizeEventType_KnownType(t *testing.T) {
	assert.Equal(t, "TenantPlanChanged", sanitizeEventType("TenantPlanChanged"))
	assert.Equal(t, "TenantQuotaAddOnChanged", sanitizeEventType("TenantQuotaAddOnChanged"))
	assert.Equal(t, "PlanCatalogChanged", sanitizeEventType("PlanCatalogChanged"))
}

func TestSanitizeEventType_UnknownType(t *testing.T) {
	assert.Equal(t, "unknown", sanitizeEventType("SomeFutureEvent"))
	assert.Equal(t, "unknown", sanitizeEventType(""))
}

// ── processOnce ───────────────────────────────────────────────────────────

func TestProcessOnce_NilInbox_CallsFnDirectly(t *testing.T) {
	ctx := context.Background()
	var called bool
	err := processOnce(ctx, nil, "consumer", makeEnv("e1", "TenantPlanChanged"), func(_ context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestProcessOnce_NilInbox_FnError_Propagates(t *testing.T) {
	ctx := context.Background()
	want := errors.New("fn error")
	err := processOnce(ctx, nil, "consumer", makeEnv("e1", "TenantPlanChanged"), func(_ context.Context) error {
		return want
	})
	assert.ErrorIs(t, err, want)
}

func TestProcessOnce_Duplicate_SkipsFn(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{duplicate: true}
	var called bool
	err := processOnce(ctx, inbox, "consumer", makeEnv("e2", "TenantPlanChanged"), func(_ context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.False(t, called, "fn must not be called for duplicate")
}

func TestProcessOnce_InboxError_Propagates(t *testing.T) {
	ctx := context.Background()
	want := errors.New("inbox error")
	inbox := &fakeInbox{err: want}
	err := processOnce(ctx, inbox, "consumer", makeEnv("e3", "TenantPlanChanged"), func(_ context.Context) error {
		return nil
	})
	assert.ErrorIs(t, err, want)
}

func TestProcessOnce_HappyPath_CallsFn(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{}
	var called bool
	err := processOnce(ctx, inbox, "consumer", makeEnv("e4", "TenantPlanChanged"), func(_ context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)
}

// ── ackUnknown ────────────────────────────────────────────────────────────

func TestAckUnknown_NilInbox_NoError(t *testing.T) {
	ctx := context.Background()
	err := ackUnknown(ctx, nil, nopConsumerLogger{}, "consumer", makeEnv("e5", "UnknownEvent"))
	require.NoError(t, err)
}

func TestAckUnknown_WithInbox_Idempotent(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{}
	err := ackUnknown(ctx, inbox, nopConsumerLogger{}, "consumer", makeEnv("e6", "UnknownEvent"))
	require.NoError(t, err)
}

// ── ProcessedEventsConsumers ──────────────────────────────────────────────

func TestProcessedEventsConsumers_ContainsAllFour(t *testing.T) {
	names := ProcessedEventsConsumers()
	assert.Contains(t, names, BillingConsumerName)
	assert.Contains(t, names, TenantConsumerName)
	assert.Contains(t, names, CatalogConsumerName)
	assert.Contains(t, names, MembershipConsumerName)
	assert.Len(t, names, 4)
}
