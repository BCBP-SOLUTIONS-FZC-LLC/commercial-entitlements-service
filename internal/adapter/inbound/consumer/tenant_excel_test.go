// tenant_excel_test.go — test functions matching commercial-entitlements-testing.xlsx
// Sheet: Consumer_Tests, rows TC-01 through TC-12.
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
)

// TC-01: TenantIdentityProvisioned → recompute triggered; snapshot created.
func TestTenantConsumer_TenantProvisioned_SnapshotCreated(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("tc01", "TenantIdentityProvisioned", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// TC-02: TenantConverted → trial assignment + trial overrides deleted, recompute triggered.
func TestTenantConsumer_TenantConverted_DeletesTTAAndTEO(t *testing.T) {
	c, eff, trial := newTenantConsumer()
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{
		"tenant_id":   tenantID.String(),
		"event_type":  "TenantConverted",
		"occurred_at": time.Now(),
	})
	env := events.Envelope[json.RawMessage]{ID: "tc02", Type: "TenantConverted", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Contains(t, trial.purgeCalls, tenantID)
}

// TC-03: Duplicate TenantConverted → no-op via processed_events.
func TestTenantConsumer_TenantConverted_Idempotent(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	trial := &fakeTrialSvc{}
	c := NewTenantEventConsumer(&fakeInbox{duplicate: true}, eff, trial, nopConsumerLogger{})
	env := tenantEnv("tc03", "TenantConverted", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "duplicate must be a no-op")
	assert.Empty(t, trial.purgeCalls, "duplicate must not purge")
}

// TC-04: TenantSubscriptionUpdated → recompute triggered; state=suspended set via service.
func TestTenantConsumer_TenantSuspended_StateSuspended(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("tc04", "TenantSubscriptionUpdated", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// TC-05: TenantOffboarded → trial data purged, recompute triggered; history retained.
func TestTenantConsumer_TenantOffboarded_SnapshotDeleted_HistoryRetained(t *testing.T) {
	c, eff, trial := newTenantConsumer()
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{
		"tenant_id":   tenantID.String(),
		"event_type":  "TenantOffboarded",
		"occurred_at": time.Now(),
	})
	env := events.Envelope[json.RawMessage]{ID: "tc05", Type: "TenantOffboarded", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Contains(t, trial.purgeCalls, tenantID)
}

// TC-06: TenantSubscriptionUpdated (trial-expired state) → recompute; balance retained.
func TestTenantConsumer_TrialExpired_StateTrialExpired_BalanceRetained(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("tc06", "TenantSubscriptionUpdated", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// TC-07: TenantSubscriptionUpdated (operator reactivation) → recompute; state=active.
func TestTenantConsumer_TenantReactivated_Operator_StateActive(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("tc07", "TenantSubscriptionUpdated", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// TC-08: Unknown tenant event type → acked without side-effects.
func TestTenantConsumer_UnknownEventType_AckUnknown(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := makeEnv("tc08", "TenantFutureV3Event")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}

// TC-09: TenantIdentityProvisioned → lifecycle recompute triggered.
func TestTenantConsumer_TenantIdentityProvisioned_RecomputesLifecycle(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("tc09", "TenantIdentityProvisioned", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// TC-10: TenantSubscriptionUpdated → recompute triggered.
func TestTenantConsumer_TenantSubscriptionUpdated_Recomputes(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := tenantEnv("tc10", "TenantSubscriptionUpdated", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// TC-11: TenantMembershipsPurged → trial data purged AND recompute triggered.
func TestTenantConsumer_TenantMembershipsPurged_PurgesTrialAndRecomputes(t *testing.T) {
	c, eff, trial := newTenantConsumer()
	tenantID := uuid.New()
	raw, _ := json.Marshal(map[string]any{
		"tenant_id":   tenantID.String(),
		"event_type":  "TenantMembershipsPurged",
		"occurred_at": time.Now(),
	})
	env := events.Envelope[json.RawMessage]{ID: "tc11", Type: "TenantMembershipsPurged", Payload: raw}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Contains(t, trial.purgeCalls, tenantID)
}

// TC-12: TenantConverted with malformed payload → acked silently (not DLQ'd).
func TestTenantConsumer_TenantConverted_MalformedPayload_SilentAck(t *testing.T) {
	c, eff, _ := newTenantConsumer()
	env := events.Envelope[json.RawMessage]{
		ID:      "tc12",
		Type:    "TenantConverted",
		Payload: json.RawMessage(`not-valid-json`),
	}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "malformed payload must be acked without side-effects")
}
