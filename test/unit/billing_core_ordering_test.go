package unit_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/consumer"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// TestBillingPlanChange_BeforeCoreRelay_SnapshotSettlesOnNewPlan is the
// B-3 (BL-OQ-28) ordering test. Billing publishes TenantPlanChanged to both
// Core and Entitlements; the two consumers are unordered. Here Entitlements
// receives Billing's event first, while Core (I-17) still reports the old
// plan, so that recompute keeps the old limits. Core then commits the new
// plan and relays TenantStateChanged{cause: TenantPlanChanged}; that relay
// must recompute so the final snapshot reflects the new plan.
func TestBillingPlanChange_BeforeCoreRelay_SnapshotSettlesOnNewPlan(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()

	var mu sync.Mutex
	corePlan := "starter" // what Core's I-17 returns right now
	iam := &fakeIAMClient{fetchSubFn: func(_ context.Context, id uuid.UUID) (*port.TenantSubscription, error) {
		mu.Lock()
		defer mu.Unlock()
		return sampleSub(id, corePlan, "active"), nil
	}}
	quota := map[string]int64{"starter": 1_000_000, "pro": 5_000_000}
	catalog := &fakeCatalogSvc{getPlanFn: func(_ context.Context, code string) (*domain.Plan, error) {
		p := samplePlan(code)
		q := quota[code]
		p.LLMTokenQuotaMonthly = &q
		return p, nil
	}}
	snaps := newSnapshotRepo()
	effectSvc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), catalog, iam, nil, newTxRunner()).
		WithLogger(port.NopLogger{})

	billing := consumer.NewBillingEventConsumer(nil, effectSvc, nil, nil, port.NopLogger{})
	membership := consumer.NewMembershipConsumer(nil, effectSvc, nil, port.NopLogger{})

	// Baseline: tenant on starter.
	_, err := effectSvc.Recompute(ctx, tenant, domain.CauseTenantLifecycle)
	require.NoError(t, err)

	// 1. Billing's TenantPlanChanged reaches Entitlements before Core applied it.
	require.NoError(t, billing.Handle(ctx, envelope(t, "billing-1", "TenantPlanChanged", map[string]any{
		"tenant_id": tenant, "plan": "pro", "previous_plan": "starter", "direction": "upgrade",
		"effective_at": time.Now().UTC(), "billing_ref": "sub_123",
	})))
	snap, err := snaps.GetLatest(ctx, tenant)
	require.NoError(t, err)
	require.Equal(t, "starter", snap.Plan, "precondition: Entitlements read I-17 before Core applied the change")

	// 2. Core applies Billing's event, commits, and relays TenantStateChanged.
	mu.Lock()
	corePlan = "pro"
	mu.Unlock()
	require.NoError(t, membership.Handle(ctx, envelope(t, "core-1", "TenantStateChanged", map[string]any{
		"tenant_id": tenant, "status": "active", "previous_status": "active",
		"plan": "pro", "previous_plan": "starter", "changed_at": time.Now().UTC(), "cause": "TenantPlanChanged",
	})))

	snap, err = snaps.GetLatest(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, "pro", snap.Plan)
	require.NotNil(t, snap.Limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
	assert.Equal(t, int64(5_000_000), *snap.Limits[domain.LimitKeyLLMTokenQuotaMonthly].Value)
}

// TestBillingPlanChange_StaleRecomputeFinishesLast_DoesNotOverwrite forces the
// worst interleaving: Entitlements' recompute for Billing's event reads I-17
// (old plan, record_version 5) and, before it writes, Core commits the new
// plan (record_version 6) and its relay's recompute writes "pro". The stale
// recompute then reaches the upsert with a higher snapshot version; the
// source_record_version guard must refuse it.
func TestBillingPlanChange_StaleRecomputeFinishesLast_DoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()

	var mu sync.Mutex
	corePlan, coreRV := "starter", int64(5)
	iam := &fakeIAMClient{fetchSubFn: func(_ context.Context, id uuid.UUID) (*port.TenantSubscription, error) {
		mu.Lock()
		defer mu.Unlock()
		sub := sampleSub(id, corePlan, "active")
		sub.RecordVersion = coreRV
		return sub, nil
	}}
	snaps := newSnapshotRepo()
	var membership *consumer.MembershipConsumer
	relayDuringStaleRecompute := false
	catalog := &fakeCatalogSvc{getPlanFn: func(_ context.Context, code string) (*domain.Plan, error) {
		// Runs after the stale recompute's I-17 read, before its upsert.
		if relayDuringStaleRecompute && code == "starter" {
			relayDuringStaleRecompute = false
			mu.Lock()
			corePlan, coreRV = "pro", 6
			mu.Unlock()
			require.NoError(t, membership.Handle(ctx, envelope(t, "core-1", "TenantStateChanged", map[string]any{
				"tenant_id": tenant, "status": "active", "previous_status": "active",
				"plan": "pro", "previous_plan": "starter", "changed_at": time.Now().UTC(), "cause": "TenantPlanChanged",
			})))
		}
		q := map[string]int64{"starter": 1_000_000, "pro": 5_000_000}[code]
		p := samplePlan(code)
		p.LLMTokenQuotaMonthly = &q
		return p, nil
	}}
	effectSvc := newEffectSvc(snaps, newOverrideRepo(), newAddonRepo(), newTrialRepo(), catalog, iam, nil, newTxRunner()).
		WithLogger(port.NopLogger{})
	billing := consumer.NewBillingEventConsumer(nil, effectSvc, nil, nil, port.NopLogger{})
	membership = consumer.NewMembershipConsumer(nil, effectSvc, nil, port.NopLogger{})

	// Baseline on starter with a different quota so the stale set differs
	// from the stored one and reaches the upsert.
	_, err := effectSvc.Recompute(ctx, tenant, domain.CauseTenantLifecycle)
	require.NoError(t, err)
	snaps.mu.Lock()
	snaps.snaps[tenant].Plan = "legacy"
	snaps.mu.Unlock()

	relayDuringStaleRecompute = true
	require.NoError(t, billing.Handle(ctx, envelope(t, "billing-1", "TenantPlanChanged", map[string]any{
		"tenant_id": tenant, "plan": "pro", "previous_plan": "starter",
		"effective_at": time.Now().UTC(), "billing_ref": "sub_123",
	})))
	require.False(t, relayDuringStaleRecompute, "precondition: the relay ran inside the stale recompute")

	snap, err := snaps.GetLatest(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, "pro", snap.Plan, "the stale recompute must not overwrite the relay's snapshot")
	assert.Equal(t, int64(6), snap.SourceRecordVersion)
}

func envelope(t *testing.T, id, eventType string, payload map[string]any) events.Envelope[json.RawMessage] {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return events.Envelope[json.RawMessage]{ID: id, Type: eventType, Payload: raw}
}
