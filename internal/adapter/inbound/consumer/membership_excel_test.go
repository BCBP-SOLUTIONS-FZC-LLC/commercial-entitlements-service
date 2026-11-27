// membership_excel_test.go — test functions matching commercial-entitlements-testing.xlsx
// Sheet: Consumer_Tests, rows MC-01 through MC-06.
package consumer

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MC-01: TenantMembershipsPurged → all data deleted (recompute triggered).
func TestMembershipConsumer_TenantMembershipsPurged_AllDataDeleted(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := membershipEnv("mc01", "TenantMembershipsPurged", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// MC-02: Duplicate TenantMembershipsPurged → no-op via processed_events.
func TestMembershipConsumer_TenantMembershipsPurged_Idempotent(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewMembershipConsumer(&fakeInbox{duplicate: true}, eff, &fakeTrialSvc{}, nopConsumerLogger{})
	env := membershipEnv("mc02", "TenantMembershipsPurged", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "duplicate must be a no-op")
}

// MC-03: Unknown membership event type → acked without side-effects.
func TestMembershipConsumer_UnknownEventType_AckUnknown(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := makeEnv("mc03", "MembershipFutureV2")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}

// MC-04: TenantStateChanged with an unrelated cause and no plan/status change → acked silently, no recompute.
func TestMembershipConsumer_TenantStateChanged_NonTrialCause_SilentAck(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := membershipEnv("mc04", "TenantStateChanged", uuid.New(), map[string]any{"cause": "Suspended"})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "non-trial cause must not trigger recompute")
}

// MC-05: TenantStateChanged with TrialReactivated cause → recompute triggered.
func TestMembershipConsumer_TenantStateChanged_TrialReactivated_Recomputes(t *testing.T) {
	c, eff := newMembershipConsumer()
	env := membershipEnv("mc05", "TenantStateChanged", uuid.New(), map[string]any{"cause": "TrialReactivated"})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// MC-06: TenantMembershipsPurged → recompute triggered; trial NOT purged by membership consumer.
func TestMembershipConsumer_TenantMembershipsPurged_RecomputeOnly_NoTrialPurge(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	trial := &fakeTrialSvc{}
	c := NewMembershipConsumer(nil, eff, trial, nopConsumerLogger{})
	env := membershipEnv("mc06", "TenantMembershipsPurged", uuid.New())
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Empty(t, trial.purgeCalls, "membership consumer must not purge trial data")
}
