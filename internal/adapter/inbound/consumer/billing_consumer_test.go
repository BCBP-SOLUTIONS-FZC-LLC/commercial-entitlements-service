package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/metrics"
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
	payload := domain.InboundBillingPlanChangedEvent{TenantID: uuid.New(), Plan: "pro", PreviousPlan: "starter", BillingRef: "sub_1"}
	env := makeBillingEnv("e1", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestBillingConsumer_TenantSubscriptionCancelled_TriggersRecompute(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingSubscriptionCancelledEvent{TenantID: uuid.New(), Reason: "customer"}
	env := makeBillingEnv("e3", "TenantSubscriptionCancelled", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestBillingConsumer_TenantReactivated_TriggersRecompute(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	payload := domain.InboundBillingReactivatedEvent{TenantID: uuid.New(), Source: "billing"}
	env := makeBillingEnv("e4", "TenantReactivated", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestBillingConsumer_MalformedPayload_ReturnsErrorForDLQ(t *testing.T) {
	c, eff, _ := newBillingConsumer()
	env := events.Envelope[json.RawMessage]{ID: "e5", Type: "TenantPlanChanged", Payload: json.RawMessage(`not-json`)}
	require.Error(t, c.Handle(context.Background(), env), "B-5: a payload that does not decode is dead-lettered, not acked")
	assert.Empty(t, eff.recomputedWith)
}

func TestBillingConsumer_SubscriptionEvent_MissingTenant_ReturnsError(t *testing.T) {
	for _, typ := range []string{"TenantPlanChanged", "TenantSubscriptionCancelled", "TenantReactivated"} {
		c, eff, _ := newBillingConsumer()
		env := events.Envelope[json.RawMessage]{ID: "e5b-" + typ, Type: typ, Payload: json.RawMessage(`{"plan":"pro"}`)}
		require.Error(t, c.Handle(context.Background(), env), typ)
		assert.Empty(t, eff.recomputedWith, typ)
	}
}

func TestBillingConsumer_SubscriptionEvents_DecodeBillingWireShape(t *testing.T) {
	tenant := uuid.New()
	for typ, body := range map[string]string{
		"TenantPlanChanged": `{"tenant_id":"` + tenant.String() + `","plan":"pro","previous_plan":"starter","direction":"upgrade",` +
			`"effective_at":"2026-11-27T10:00:00Z","billing_ref":"sub_1","billing_subscription_id":"` + uuid.NewString() + `"}`,
		"TenantSubscriptionCancelled": `{"tenant_id":"` + tenant.String() + `","cancelled_at":"2026-11-27T10:00:00Z","reason":"customer"}`,
		"TenantReactivated":           `{"tenant_id":"` + tenant.String() + `","source":"billing","reactivated_at":"2026-11-27T10:00:00Z","previous_status":"past_due"}`,
	} {
		c, eff, _ := newBillingConsumer()
		env := events.Envelope[json.RawMessage]{ID: "wire-" + typ, Type: typ, Payload: json.RawMessage(body)}
		require.NoError(t, c.Handle(context.Background(), env), typ)
		require.Len(t, eff.recomputedWith, 1, typ)
		assert.Equal(t, domain.CauseBillingEvent, eff.recomputedWith[0], typ)
	}
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

func TestBillingConsumer_AddonRefund_MalformedPayload_DLQ(t *testing.T) {
	c, _, _ := newBillingConsumer()
	// Valid reason peek ("refund") but the full payload has a bad delta type,
	// causing json.Unmarshal of InboundBillingAddonRefundEvent to fail.
	env := events.Envelope[json.RawMessage]{
		ID: "e_refund_bad", Type: "TenantQuotaAddOnChanged",
		Payload: json.RawMessage(`{"reason":"refund","delta":"not-a-number"}`),
	}
	err := c.Handle(context.Background(), env)
	assert.Error(t, err, "malformed refund payload must return error to trigger NACK/DLQ")
}

func TestBillingConsumer_AddonRefund_ServiceError_PropagatesNACK(t *testing.T) {
	_, _, addon := newBillingConsumer()
	addon.applyRefundErr = errors.New("refund service failure")
	c := NewBillingEventConsumer(nil, &fakeEffectiveSvc{}, nil, addon, nopConsumerLogger{})
	env := makeBillingEnv("e_refund_svc_err", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id":    uuid.New().String(),
		"resource":     "llm_tokens",
		"delta":        int64(-10_000),
		"reason":       "refund",
		"billing_ref":  "ref_err",
		"purchase_ref": uuid.New().String(),
	})
	err := c.Handle(context.Background(), env)
	assert.Error(t, err, "ApplyBillingRefund error must propagate as NACK")
	assert.True(t, addon.refundCalled)
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
	payload := domain.InboundBillingPlanChangedEvent{TenantID: uuid.New(), Plan: "pro", PreviousPlan: "starter", BillingRef: "sub_1"}
	env := makeBillingEnv("e14", "TenantPlanChanged", payload)
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, eff.recomputedWith, "duplicate must skip the fn")
}

// ── B-8: re-claimed billing_ref ───────────────────────────────────────────

func TestBillingConsumer_AddonPurchase_BillingRefClaimed_CountsAndAcks(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_billing_ref_conflict_total"}, []string{"event_type", "reason"})
	saved := metrics.BillingRefConflictTotal
	metrics.BillingRefConflictTotal = counter
	t.Cleanup(func() { metrics.BillingRefConflictTotal = saved })

	c, _, addon := newBillingConsumer()
	addon.applyPurchaseErr = domain.NewError(domain.ErrBillingRefAlreadyClaimed, "billing_ref already claimed")
	env := makeBillingEnv("b8", "TenantQuotaAddOnChanged", domain.InboundBillingAddonPurchaseEvent{
		TenantID: uuid.New(), Resource: "llm_tokens", Delta: 100_000, Reason: "purchase",
		BillingRef: "pur_dup", PurchasedAt: time.Now().UTC(),
	})

	require.NoError(t, c.Handle(context.Background(), env), "a conflict cannot succeed on retry, so it is acked")
	assert.True(t, addon.purchaseCalled)
	reg := prometheus.NewRegistry()
	reg.MustRegister(counter)
	mfs, err := reg.Gather()
	require.NoError(t, err)
	require.Len(t, mfs, 1)
	require.Len(t, mfs[0].GetMetric(), 1)
	m := mfs[0].GetMetric()[0]
	assert.Equal(t, 1.0, m.GetCounter().GetValue())
	assert.Equal(t, "TenantQuotaAddOnChanged", m.GetLabel()[0].GetValue())
	assert.Equal(t, "purchase", m.GetLabel()[1].GetValue())
}

func TestBillingConsumer_AddonPurchase_OtherError_Retries(t *testing.T) {
	c, _, addon := newBillingConsumer()
	addon.applyPurchaseErr = errors.New("db down")
	env := makeBillingEnv("b8b", "TenantQuotaAddOnChanged", domain.InboundBillingAddonPurchaseEvent{
		TenantID: uuid.New(), Resource: "llm_tokens", Delta: 100_000, Reason: "purchase", BillingRef: "pur_x",
	})
	require.Error(t, c.Handle(context.Background(), env))
}
