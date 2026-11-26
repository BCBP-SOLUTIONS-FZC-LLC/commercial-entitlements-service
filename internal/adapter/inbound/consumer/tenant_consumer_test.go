package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

var errTenantStorage = errors.New("storage failure")

func newTenantConsumer() (*TenantEventConsumer, *fakeEffectiveSvc, *fakeTrialSvc) {
	eff := &fakeEffectiveSvc{}
	trial := &fakeTrialSvc{}
	c := NewTenantEventConsumer(nil, eff, trial, nopConsumerLogger{})
	return c, eff, trial
}

func tenantEnv(id, eventType string, tenantID uuid.UUID) events.Envelope[json.RawMessage] {
	// Payload contains only tenant_id — matching iam-realm-provisioner wire shape.
	raw, _ := json.Marshal(map[string]any{"tenant_id": tenantID.String()})
	return events.Envelope[json.RawMessage]{ID: id, Type: eventType, Payload: raw}
}

// ── TenantIdentityProvisioned ─────────────────────────────────────────────

func TestTenantConsumer_TenantIdentityProvisioned_TriggersRecompute(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("t1", "TenantIdentityProvisioned", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Equal(t, domain.CauseTenantLifecycle, eff.recomputedWith[0])
}

func TestTenantConsumer_TenantSubscriptionUpdated_TriggersRecompute(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("t2", "TenantSubscriptionUpdated", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestTenantConsumer_TenantIdentityProvisioned_MalformedPayload_Acks(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := events.Envelope[json.RawMessage]{ID: "t3", Type: "TenantIdentityProvisioned", Payload: json.RawMessage(`bad`)}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}

// ── Offboard events ───────────────────────────────────────────────────────

func TestTenantConsumer_TenantOffboarded_PurgesTrialAndRecomputes(t *testing.T) {
	c, eff, trial := newTenantConsumer()
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{
		"tenant_id": tenantID.String(), "event_type": "TenantOffboarded", "occurred_at": time.Now(),
	})
	env := events.Envelope[json.RawMessage]{ID: "t4", Type: "TenantOffboarded", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Contains(t, trial.purgeCalls, tenantID)
	assert.Len(t, eff.recomputedWith, 1)
}

func TestTenantConsumer_TenantConverted_PurgesTrialAndRecomputes(t *testing.T) {
	c, eff, trial := newTenantConsumer()
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{"tenant_id": tenantID.String()})
	env := events.Envelope[json.RawMessage]{ID: "t5", Type: "TenantConverted", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, trial.purgeCalls, 1)
	assert.Len(t, eff.recomputedWith, 1)
}

func TestTenantConsumer_TenantMembershipsPurged_Recomputes(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{"tenant_id": tenantID.String()})
	env := events.Envelope[json.RawMessage]{ID: "t6", Type: "TenantMembershipsPurged", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestTenantConsumer_Offboard_MalformedPayload_Acks(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := events.Envelope[json.RawMessage]{ID: "t7", Type: "TenantOffboarded", Payload: json.RawMessage(`bad`)}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}

func TestTenantConsumer_Offboard_PurgeError_NonFatal(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	trial := &fakeTrialSvc{purgeErr: errTenantStorage}
	c := NewTenantEventConsumer(nil, eff, trial, nopConsumerLogger{})
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{"tenant_id": tenantID.String()})
	env := events.Envelope[json.RawMessage]{ID: "t8", Type: "TenantOffboarded", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1, "recompute must still happen even if purge fails")
}

func TestTenantConsumer_NilTrialSvc_NoError(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewTenantEventConsumer(nil, eff, nil, nopConsumerLogger{})
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{"tenant_id": tenantID.String()})
	env := events.Envelope[json.RawMessage]{ID: "t9", Type: "TenantOffboarded", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// ── Realm-Provisioner lifecycle events (CD-6, CD-49) ─────────────────────

func TestTenantConsumer_TenantSuspended_TriggersRecompute(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("ts1", "TenantSuspended", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Equal(t, domain.CauseTenantLifecycle, eff.recomputedWith[0])
}

func TestTenantConsumer_TenantReactivated_TriggersRecompute(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("tr1", "TenantReactivated", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Equal(t, domain.CauseTenantLifecycle, eff.recomputedWith[0])
}

func TestTenantConsumer_TrialExpired_TriggersRecompute(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("te1", "TrialExpired", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Equal(t, domain.CauseTenantLifecycle, eff.recomputedWith[0])
}

func TestTenantConsumer_RPLifecycle_MalformedPayload_Acks(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	for _, evType := range []string{"TenantSuspended", "TenantReactivated", "TrialExpired"} {
		env := events.Envelope[json.RawMessage]{ID: evType, Type: evType, Payload: json.RawMessage(`bad`)}
		require.NoError(t, c.Handle(context.Background(), env), evType)
	}
	assert.Empty(t, eff.recomputedWith)
}

// ── Unknown event ─────────────────────────────────────────────────────────

func TestTenantConsumer_UnknownEvent_Acks(t *testing.T) {
	c, _, _ := newTenantConsumer()
	env := makeEnv("t99", "SomeFutureEvent")
	require.NoError(t, c.Handle(context.Background(), env))
}

// ── With inbox ────────────────────────────────────────────────────────────

func TestTenantConsumer_WithInbox_Duplicate_SkipsFn(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewTenantEventConsumer(&fakeInbox{duplicate: true}, eff, nil, nopConsumerLogger{})
	env := tenantEnv("t10", "TenantIdentityProvisioned", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}
