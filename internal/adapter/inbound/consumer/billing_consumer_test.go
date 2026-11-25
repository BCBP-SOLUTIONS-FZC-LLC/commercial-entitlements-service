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

func makeBillingEnv(id, eventType string, payload any) events.Envelope[json.RawMessage] {
	raw, _ := json.Marshal(payload)
	return events.Envelope[json.RawMessage]{ID: id, Type: eventType, Payload: raw}
}

func newBillingConsumer() (*BillingEventConsumer, *fakeEffectiveSvc, *fakeAddonSvc) {
	eff := &fakeEffectiveSvc{}
	addon := &fakeAddonSvc{}
	c := NewBillingEventConsumer(nil, eff, nil, addon, nopConsumerLogger{})
	return c, eff, addon
}

// ── Handle — subscription events ─────────────────────────────────────────

func TestBillingConsumer_TenantPlanChanged_TriggersRecompute(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingEvent{TenantID: uuid.New(), SubscriptionState: "active", PlanCode: "pro"}
	env := makeBillingEnv("e1", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestBillingConsumer_TenantPlanChanged_WithTrialProfile_CauseTrial(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	tp := "reduced_plan"
	payload := domain.InboundBillingEvent{TenantID: uuid.New(), SubscriptionState: "trial", PlanCode: "starter", TrialProfile: &tp}
	env := makeBillingEnv("e2", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Equal(t, domain.CauseTrialChange, eff.recomputedWith[0])
}

func TestBillingConsumer_TenantSubscriptionCancelled_TriggersRecompute(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingEvent{TenantID: uuid.New()}
	env := makeBillingEnv("e3", "TenantSubscriptionCancelled", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestBillingConsumer_TenantReactivated_TriggersRecompute(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingEvent{TenantID: uuid.New()}
	env := makeBillingEnv("e4", "TenantReactivated", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestBillingConsumer_MalformedPayload_AcksWithoutError(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	env := events.Envelope[json.RawMessage]{ID: "e5", Type: "TenantPlanChanged", Payload: json.RawMessage(`not-json`)}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "malformed payload must be acked without recompute")
}

// ── Handle — addon purchase ───────────────────────────────────────────────

func TestBillingConsumer_AddonPurchase_CallsApplyBillingPurchase(t *testing.T) {
	c, _, addon := newBillingConsumer()
	payload := domain.InboundBillingAddonPurchaseEvent{
		TenantID:    uuid.New(),
		Resource:    "llm_tokens",
		Delta:       100_000,
		Reason:      "purchase",
		BillingRef:  "inv_001",
		PurchasedAt: time.Now().UTC(),
	}
	env := makeBillingEnv("e6", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id":    payload.TenantID.String(),
		"resource":     payload.Resource,
		"delta":        payload.Delta,
		"reason":       "purchase",
		"billing_ref":  payload.BillingRef,
		"purchased_at": payload.PurchasedAt,
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.True(t, addon.purchaseCalled)
}

func TestBillingConsumer_AddonPurchase_NonPositiveDelta_Skipped(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("e7", "TenantQuotaAddOnChanged", map[string]any{
		"reason": "purchase",
		"delta":  int64(0),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.False(t, addon.purchaseCalled)
}

func TestBillingConsumer_AddonPurchase_MalformedPayload_DLQ(t *testing.T) {
	c, _, _ := newBillingConsumer()
	env := events.Envelope[json.RawMessage]{
		ID: "e8", Type: "TenantQuotaAddOnChanged",
		Payload: json.RawMessage(`{"reason":"purchase","delta":"not-a-number"}`),
	}
	// unmarshal error on the purchase payload → DLQ (error returned)
	err := c.Handle(context.Background(), env)
	// malformed json.RawMessage for purchase returns fmt.Errorf
	assert.Error(t, err)
}

// ── Handle — addon refund ─────────────────────────────────────────────────

func TestBillingConsumer_AddonRefund_CallsApplyBillingRefund(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("e9", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id":    uuid.New().String(),
		"resource":     "llm_tokens",
		"delta":        int64(-50_000),
		"reason":       "refund",
		"billing_ref":  "ref_xyz",
		"purchase_ref": uuid.New().String(),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.True(t, addon.refundCalled)
}

func TestBillingConsumer_AddonRefund_NonNegativeDelta_Skipped(t *testing.T) {
	c, _, addon := newBillingConsumer()
	env := makeBillingEnv("e10", "TenantQuotaAddOnChanged", map[string]any{
		"reason": "refund",
		"delta":  int64(100),
	})
	require.NoError(t, c.Handle(context.Background(), env))
	assert.False(t, addon.refundCalled)
}

// ── Handle — addon unknown reason ─────────────────────────────────────────

func TestBillingConsumer_AddonUnknownReason_AcksWithoutError(t *testing.T) {
	c, _, _ := newBillingConsumer()
	env := makeBillingEnv("e11", "TenantQuotaAddOnChanged", map[string]any{
		"reason": "credit",
		"delta":  int64(100),
	})
	require.NoError(t, c.Handle(context.Background(), env))
}

func TestBillingConsumer_AddonMalformedReasonPeek_DLQ(t *testing.T) {
	c, _, _ := newBillingConsumer()
	env := events.Envelope[json.RawMessage]{
		ID: "e12", Type: "TenantQuotaAddOnChanged",
		Payload: json.RawMessage(`not-json-at-all`),
	}
	err := c.Handle(context.Background(), env)
	assert.Error(t, err)
}

// ── Handle — unknown event type ───────────────────────────────────────────

func TestBillingConsumer_UnknownEventType_AcksWithoutError(t *testing.T) {
	c, _, _ := newBillingConsumer()
	env := makeEnv("e13", "SomeFutureEvent")
	require.NoError(t, c.Handle(context.Background(), env))
}

// ── Handle — with inbox (dedup) ───────────────────────────────────────────

func TestBillingConsumer_WithInbox_Duplicate_SkipsFn(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewBillingEventConsumer(&fakeInbox{duplicate: true}, eff, nil, &fakeAddonSvc{}, nopConsumerLogger{})
	payload := domain.InboundBillingEvent{TenantID: uuid.New()}
	env := makeBillingEnv("e14", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "duplicate must skip the fn")
}
