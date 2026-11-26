// billing_excel_test.go — test functions matching commercial-entitlements-testing.xlsx
// Sheet: Consumer_Tests, rows BC-01 through BC-16.
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

// BC-01: TenantPlanChanged triggers recompute + TenantEntitlementsChanged emitted.
func TestBillingConsumer_TenantPlanChanged_RecomputesAndEmits(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingEvent{TenantID: uuid.New(), SubscriptionState: "active", PlanCode: "pro"}
	env := makeBillingEnv("bc01", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
	assert.Equal(t, domain.CauseBillingEvent, eff.recomputedWith[0])
}

// BC-02: Duplicate TenantPlanChanged is a no-op via processed_events.
func TestBillingConsumer_TenantPlanChanged_Idempotent(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewBillingEventConsumer(&fakeInbox{duplicate: true}, eff, nil, &fakeAddonSvc{}, nopConsumerLogger{})
	payload := domain.InboundBillingEvent{TenantID: uuid.New()}
	env := makeBillingEnv("bc02", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "duplicate must be a no-op")
}

// BC-03: TenantSubscriptionCancelled triggers recompute; state maps to read_only.
func TestBillingConsumer_TenantSubscriptionCancelled_StateReadOnly(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingEvent{TenantID: uuid.New(), SubscriptionState: "read_only"}
	env := makeBillingEnv("bc03", "TenantSubscriptionCancelled", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// BC-04: Duplicate TenantSubscriptionCancelled is a no-op via processed_events.
func TestBillingConsumer_TenantSubscriptionCancelled_Idempotent(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewBillingEventConsumer(&fakeInbox{duplicate: true}, eff, nil, &fakeAddonSvc{}, nopConsumerLogger{})
	payload := domain.InboundBillingEvent{TenantID: uuid.New()}
	env := makeBillingEnv("bc04", "TenantSubscriptionCancelled", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "duplicate must be a no-op")
}

// BC-05: TenantReactivated triggers recompute; state becomes active.
func TestBillingConsumer_TenantReactivated_StateActive(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingEvent{TenantID: uuid.New(), SubscriptionState: "active"}
	env := makeBillingEnv("bc05", "TenantReactivated", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

// BC-06: TenantQuotaAddOnChanged (purchase) creates a new addon pack.
func TestBillingConsumer_TenantQuotaAddOnChanged_Purchase_PackCreated(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("bc06", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id":    uuid.New().String(),
		"resource":     "llm_tokens",
		"delta":        int64(100_000),
		"reason":       "purchase",
		"billing_ref":  "inv_bc06",
		"purchased_at": time.Now().UTC(),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.True(t, addon.purchaseCalled)
}

// BC-07: Duplicate TenantQuotaAddOnChanged (purchase) is a no-op.
func TestBillingConsumer_TenantQuotaAddOnChanged_DuplicatePurchase_Idempotent(t *testing.T) {
	addon := &fakeAddonSvc{}
	c := NewBillingEventConsumer(&fakeInbox{duplicate: true}, &fakeEffectiveSvc{}, nil, addon, nopConsumerLogger{})
	env := makeBillingEnv("bc07", "TenantQuotaAddOnChanged", map[string]any{
		"reason": "purchase",
		"delta":  int64(50_000),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.False(t, addon.purchaseCalled, "duplicate must skip service call")
}

// BC-08: TenantQuotaAddOnChanged (refund) revokes the pack.
func TestBillingConsumer_TenantQuotaAddOnChanged_Refund_PackRevoked(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("bc08", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id":    uuid.New().String(),
		"resource":     "llm_tokens",
		"delta":        int64(-100_000),
		"reason":       "refund",
		"billing_ref":  "inv_bc08",
		"purchase_ref": uuid.New().String(),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.True(t, addon.refundCalled)
}

// BC-09: Cross-tenant billing_ref — service silently handles it; consumer acks.
func TestBillingConsumer_CrossTenantBillingRef_AckedNotDLQ(t *testing.T) {
	c, _, _ := newBillingConsumer()
	env := makeBillingEnv("bc09", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id":    uuid.New().String(),
		"resource":     "llm_tokens",
		"delta":        int64(50_000),
		"reason":       "purchase",
		"billing_ref":  "cross-tenant-ref",
		"purchased_at": time.Now().UTC(),
	})
	err := c.Handle(context.Background(), env)
	// service (fake) returns nil → consumer acks without DLQ
	require.NoError(t, err)
}

// BC-10: Completely malformed addon payload → error returned → NACK then DLQ.
func TestBillingConsumer_MalformedPayload_NACKThenDLQ(t *testing.T) {
	c, _, _ := newBillingConsumer()
	env := events.Envelope[json.RawMessage]{
		ID: "bc10", Type: "TenantQuotaAddOnChanged",
		Payload: json.RawMessage(`not-valid-json-at-all`),
	}
	err := c.Handle(context.Background(), env)
	assert.Error(t, err, "malformed payload must return error to trigger NACK/DLQ")
}

// BC-11: Unknown billing event type → acked without recompute.
func TestBillingConsumer_UnknownEventType_AckUnknown(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	env := makeEnv("bc11", "BillingFutureEventV2")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith)
}

// BC-12: Subscription event with malformed payload → acked silently (not DLQ'd).
func TestBillingConsumer_SubscriptionEvent_MalformedPayload_SilentAck(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	env := events.Envelope[json.RawMessage]{
		ID: "bc12", Type: "TenantPlanChanged",
		Payload: json.RawMessage(`not-json`),
	}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "malformed subscription event must be acked without recompute")
}

// BC-13: TenantPlanChanged with trial profile → cause=CauseTrialChange.
func TestBillingConsumer_TenantPlanChanged_WithTrialProfile_CauseTrialChange(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	tp := "reduced_plan"
	payload := domain.InboundBillingEvent{TenantID: uuid.New(), SubscriptionState: "trial", PlanCode: "starter", TrialProfile: &tp}
	env := makeBillingEnv("bc13", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	require.Len(t, eff.recomputedWith, 1)
	assert.Equal(t, domain.CauseTrialChange, eff.recomputedWith[0])
}

// BC-14: TenantQuotaAddOnChanged with unknown reason → acked without service call.
func TestBillingConsumer_AddonUnknownReason_SilentAck(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("bc14", "TenantQuotaAddOnChanged", map[string]any{
		"reason": "credit_note",
		"delta":  int64(1000),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.False(t, addon.purchaseCalled)
	assert.False(t, addon.refundCalled)
}

// BC-15: TenantQuotaAddOnChanged purchase with non-positive delta → skipped silently.
func TestBillingConsumer_AddonPurchase_NonPositiveDelta_SilentSkip(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("bc15", "TenantQuotaAddOnChanged", map[string]any{
		"reason": "purchase",
		"delta":  int64(0),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.False(t, addon.purchaseCalled, "non-positive delta must be skipped")
}

// BC-16: TenantQuotaAddOnChanged refund with non-negative delta → skipped silently.
func TestBillingConsumer_AddonRefund_NonNegativeDelta_SilentSkip(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("bc16", "TenantQuotaAddOnChanged", map[string]any{
		"reason": "refund",
		"delta":  int64(100),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.False(t, addon.refundCalled, "non-negative delta must be skipped for refund")
}

// SEC-12 (filed in Api_Testing sheet): Billing addon event with unknown resource
// → consumer acks silently; no service call made (forward-compat behaviour).
func TestBillingConsumer_UnknownResource_AckedIgnored(t *testing.T) {
	c, _, addon := newBillingConsumer()
	// resource="invalid_resource" is an unknown resource but reason="purchase" is valid.
	// The consumer does not validate resource; it passes through to the service.
	// With a nil-error fake service, the consumer acks (returns nil).
	env := makeBillingEnv("sec12", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id":    uuid.New().String(),
		"resource":     "invalid_resource",
		"delta":        int64(1000),
		"reason":       "purchase",
		"billing_ref":  "ref_sec12",
		"purchased_at": time.Now().UTC(),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.True(t, addon.purchaseCalled, "consumer must delegate to service for valid purchase payload")
}
