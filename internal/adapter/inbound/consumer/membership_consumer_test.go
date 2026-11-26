package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

func newMembershipConsumer() (*MembershipConsumer, *fakeEffectiveSvc) {
	eff := &fakeEffectiveSvc{}
	c := NewMembershipConsumer(nil, eff, &fakeTrialSvc{}, nopConsumerLogger{})
	return c, eff
}

func membershipEnv(id, eventType string, tenantID uuid.UUID, extra ...map[string]any) events.Envelope[json.RawMessage] {
	body := map[string]any{"tenant_id": tenantID.String(), "event_type": eventType, "occurred_at": time.Now()}
	for _, m := range extra {
		for k, v := range m {
			body[k] = v
		}
	}
	raw, _ := json.Marshal(body)
	return events.Envelope[json.RawMessage]{ID: id, Type: eventType, Payload: raw}
}

// ── TenantMembershipsPurged ───────────────────────────────────────────────

func TestMembershipConsumer_TenantMembershipsPurged_TriggersRecompute(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := membershipEnv("m1", "TenantMembershipsPurged", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Equal(t, domain.CauseTenantLifecycle, eff.recomputedWith[0])
}

func TestMembershipConsumer_TenantMembershipsPurged_MalformedPayload_Acks(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := events.Envelope[json.RawMessage]{ID: "m2", Type: "TenantMembershipsPurged", Payload: json.RawMessage(`not-json`)}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}

// ── TenantStateChanged ────────────────────────────────────────────────────

func TestMembershipConsumer_TenantStateChanged_TrialReactivated_TriggersRecompute(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := membershipEnv("m3", "TenantStateChanged", uuid.New(), map[string]any{"cause": "TrialReactivated"})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestMembershipConsumer_TenantStateChanged_OtherCause_Acks(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := membershipEnv("m4", "TenantStateChanged", uuid.New(), map[string]any{"cause": "Suspended"})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "non-TrialReactivated cause must be acked without recompute")
}

func TestMembershipConsumer_TenantStateChanged_MalformedPayload_Acks(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := events.Envelope[json.RawMessage]{ID: "m5", Type: "TenantStateChanged", Payload: json.RawMessage(`bad`)}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}

// ── Unknown event ─────────────────────────────────────────────────────────

func TestMembershipConsumer_UnknownEvent_Acks(t *testing.T) {
	c, _ := newMembershipConsumer()
	env := makeEnv("m99", "SomeOtherEvent")
	require.NoError(t, c.Handle(context.Background(), env))
}

// ── With inbox ────────────────────────────────────────────────────────────

func TestMembershipConsumer_WithInbox_Duplicate_SkipsFn(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewMembershipConsumer(&fakeInbox{duplicate: true}, eff, &fakeTrialSvc{}, nopConsumerLogger{})
	env := membershipEnv("m10", "TenantMembershipsPurged", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}
