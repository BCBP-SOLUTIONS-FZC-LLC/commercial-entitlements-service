//go:build integration

// Phase 5 · P5-REFUND — Billing add-on refunds are idempotent on billing_ref
// (Billing refund_id) through the quota_addon_refunds ledger (F-3, BL-OQ-20
// W-5), not only on the envelope ID that processed_events prunes after
// ProcessedEventsTTLDays.
package postgres_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/test/dbseed"
)

// refundIAM / refundCatalog are the only stubs: the tenant is active on a plan
// with a fixed quota, so Recompute runs against the real snapshot repository.
type refundIAM struct{}

func (refundIAM) FetchPlan(context.Context, string) (*domain.Plan, error) { return refundPlan(), nil }
func (refundIAM) FetchAllPlans(context.Context) ([]*domain.Plan, error) {
	return []*domain.Plan{refundPlan()}, nil
}
func (refundIAM) FetchTenantSubscription(_ context.Context, id uuid.UUID) (*port.TenantSubscription, error) {
	return &port.TenantSubscription{TenantID: id, PlanCode: "pro", State: "active", RecordVersion: 1}, nil
}

type refundCatalog struct{}

func (refundCatalog) GetPlan(context.Context, string) (*domain.Plan, error) { return refundPlan(), nil }
func (refundCatalog) RefreshPlan(context.Context, string) (*domain.Plan, error) {
	return refundPlan(), nil
}
func (refundCatalog) FanoutCatalogRefresh(context.Context, string) error { return nil }
func (refundCatalog) ListPlans(context.Context) ([]string, error)        { return []string{"pro"}, nil }

func refundPlan() *domain.Plan {
	q, b := int64(1_000_000), int64(50_000)
	return &domain.Plan{Code: "pro", DisplayName: "Pro", LLMTokenQuotaMonthly: &q, APIRequestBudgetMonthly: &b,
		APIRateLimitRPS: 50, ModelTier: "standard", RecordVersion: 1}
}

type refundEnv struct {
	appPool *pgcommon.Pool
	seed    *dbseed.Pool
	inbox   *pgadapter.IdempotencyRepository
	addons  *pgadapter.AddonRepository
	svc     *service.AddonService
}

func newRefundEnv(t *testing.T) (*refundEnv, uuid.UUID) {
	t.Helper()
	appPool, seed, _, tenantID := setupTestDB(t)
	tx := pgadapter.NewTxRunner(appPool, idempotencyNoopPublisher{})
	addons := pgadapter.NewAddonRepository(appPool)
	effectSvc := service.NewEffectiveSetService(pgadapter.NewSnapshotRepository(appPool), pgadapter.NewOverrideRepository(appPool),
		addons, pgadapter.NewTrialRepository(appPool), refundCatalog{}, refundIAM{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewAddonService(addons, pgadapter.NewDrawdownRepository(appPool), pgadapter.NewShortfallRepository(appPool), effectSvc, tx).
		WithLogger(port.NopLogger{}).WithRefundRepository(pgadapter.NewRefundRepository(appPool))
	inbox, err := pgadapter.NewIdempotencyRepository(appPool, idempotencyNoopPublisher{}, "billing-entitlements-q")
	require.NoError(t, err)
	return &refundEnv{appPool: appPool, seed: seed, inbox: inbox, addons: addons, svc: svc}, tenantID
}

// grantPack inserts the pack a Billing purchase created; its billing_ref is
// the purchase_id that refunds reference as purchase_ref.
func (e *refundEnv) grantPack(t *testing.T, ctx context.Context, tenantID uuid.UUID, remaining int64) (uuid.UUID, uuid.UUID) {
	t.Helper()
	purchaseRef := uuid.New()
	ref := purchaseRef.String()
	a := sampleAddon(tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	a.TotalTokens, a.RemainingTokens, a.BillingRef = 50_000, remaining, &ref
	require.NoError(t, e.addons.Insert(ctx, a))
	return a.ID, purchaseRef
}

// deliver runs one refund delivery exactly as the Billing consumer does:
// inside the inbox transaction for envelope eventID.
func (e *refundEnv) deliver(ctx context.Context, eventID string, req port.ApplyBillingRefundRequest) error {
	_, err := e.inbox.ProcessOnce(ctx, eventID, "TenantQuotaAddOnChanged", func(txCtx context.Context) error {
		return e.svc.ApplyBillingRefund(txCtx, req)
	})
	return err
}

func (e *refundEnv) remaining(t *testing.T, ctx context.Context, addonID uuid.UUID) int64 {
	t.Helper()
	a, err := e.addons.GetByID(ctx, addonID)
	require.NoError(t, err)
	return a.RemainingTokens
}

func (e *refundEnv) refundRows(t *testing.T, billingRef uuid.UUID) (n int, tokens int64) {
	t.Helper()
	require.NoError(t, e.seed.RunQueryRow(context.Background(), func(r pgcommon.Row) error { return r.Scan(&n, &tokens) },
		`SELECT count(*), coalesce(sum(tokens_removed),0) FROM quota_addon_refunds WHERE billing_ref = $1`, billingRef))
	return n, tokens
}

// Test Case ID:      P5-REFUND-001
// Scenario:          The same refund (billing_ref R) arrives under two different envelope IDs
// Expected Result:   Tokens reduced once; one ledger row; second delivery acked
func TestP5Refund001_SameBillingRef_DifferentEnvelopes_AppliedOnce(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)
	addonID, purchaseRef := e.grantPack(t, ctx, tenantID, 50_000)
	req := port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -10_000, BillingRef: uuid.New()}

	require.NoError(t, e.deliver(ctx, uuid.NewString(), req))
	require.NoError(t, e.deliver(ctx, uuid.NewString(), req), "replay under a new envelope ID is acked")

	assert.Equal(t, int64(40_000), e.remaining(t, ctx, addonID))
	n, tokens := e.refundRows(t, req.BillingRef)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(10_000), tokens)
}

// Test Case ID:      P5-REFUND-002
// Scenario:          Same envelope redelivered after its processed_events row is pruned
// Expected Result:   Tokens reduced once
func TestP5Refund002_RedeliveryAfterProcessedEventsPrune_AppliedOnce(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)
	addonID, purchaseRef := e.grantPack(t, ctx, tenantID, 50_000)
	eventID := uuid.NewString()
	req := port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -10_000, BillingRef: uuid.New()}

	require.NoError(t, e.deliver(ctx, eventID, req))
	require.NoError(t, e.seed.Exec(context.Background(), `DELETE FROM processed_events`)) // simulate the 8-day prune
	require.NoError(t, e.deliver(ctx, eventID, req))

	assert.Equal(t, int64(40_000), e.remaining(t, ctx, addonID))
	n, _ := e.refundRows(t, req.BillingRef)
	assert.Equal(t, 1, n)
}

// Test Case ID:      P5-REFUND-003
// Scenario:          Two refunds R1, R2 for the same purchase_ref
// Expected Result:   Both applied, each capped by the remaining unused tokens; two rows
func TestP5Refund003_TwoRefundsSamePurchase_BothApplied(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)
	addonID, purchaseRef := e.grantPack(t, ctx, tenantID, 15_000)
	r1 := port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -10_000, BillingRef: uuid.New()}
	r2 := port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -10_000, BillingRef: uuid.New()}

	require.NoError(t, e.deliver(ctx, uuid.NewString(), r1))
	require.NoError(t, e.deliver(ctx, uuid.NewString(), r2))

	assert.Equal(t, int64(0), e.remaining(t, ctx, addonID))
	_, t1 := e.refundRows(t, r1.BillingRef)
	_, t2 := e.refundRows(t, r2.BillingRef)
	assert.Equal(t, int64(10_000), t1)
	assert.Equal(t, int64(5_000), t2, "capped at the remaining unused tokens")
}

// Test Case ID:      P5-REFUND-004
// Scenario:          Refund R recorded for tenant A arrives for tenant B, inside the inbox tx
// Expected Result:   ErrBillingRefAlreadyClaimed; B's pack untouched; when the consumer
//
//	acks, the inbox transaction commits (not aborted)
func TestP5Refund004_BillingRefOfOtherTenant_ConflictAndTxCommits(t *testing.T) {
	t.Parallel()
	e, tenantA := newRefundEnv(t)
	ctxA := withTenant(context.Background(), tenantA)
	_, purchaseA := e.grantPack(t, ctxA, tenantA, 50_000)
	ref := uuid.New()
	require.NoError(t, e.deliver(ctxA, uuid.NewString(),
		port.ApplyBillingRefundRequest{TenantID: tenantA, PurchaseRef: purchaseA, Delta: -1_000, BillingRef: ref}))

	tenantB := uuid.New()
	ctxB := withTenant(context.Background(), tenantB)
	addonB, purchaseB := e.grantPack(t, ctxB, tenantB, 50_000)
	var svcErr error
	eventID := uuid.NewString()
	dup, err := e.inbox.ProcessOnce(ctxB, eventID, "TenantQuotaAddOnChanged", func(txCtx context.Context) error {
		svcErr = e.svc.ApplyBillingRefund(txCtx,
			port.ApplyBillingRefundRequest{TenantID: tenantB, PurchaseRef: purchaseB, Delta: -1_000, BillingRef: ref})
		return nil // the consumer acks ErrBillingRefAlreadyClaimed
	})
	require.NoError(t, err, "the inbox transaction must commit after the conflict")
	assert.False(t, dup)
	assert.ErrorIs(t, svcErr, domain.ErrBillingRefAlreadyClaimed)
	assert.Equal(t, int64(50_000), e.remaining(t, ctxB, addonB))
	n, tokens := e.refundRows(t, ref)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(1_000), tokens, "tenant A's row unchanged")

	// The claim committed: the same envelope is now a duplicate.
	dup, err = e.inbox.ProcessOnce(ctxB, eventID, "TenantQuotaAddOnChanged", func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, dup)
}

// Test Case ID:      P5-REFUND-005
// Scenario:          Refund when remaining_tokens = 0, then redelivered after tokens free up
// Expected Result:   Row written with tokens_removed = 0; the redelivery changes nothing
func TestP5Refund005_NothingUnused_ZeroRow_RedeliveryNoop(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)
	addonID, purchaseRef := e.grantPack(t, ctx, tenantID, 0)
	req := port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -5_000, BillingRef: uuid.New()}

	require.NoError(t, e.deliver(ctx, uuid.NewString(), req))
	n, tokens := e.refundRows(t, req.BillingRef)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(0), tokens)

	require.NoError(t, e.seed.Exec(context.Background(),
		`UPDATE quota_addons SET remaining_tokens = 3000 WHERE id = $1`, addonID))
	require.NoError(t, e.deliver(ctx, uuid.NewString(), req))
	assert.Equal(t, int64(3_000), e.remaining(t, ctx, addonID))
}

// Test Case ID:      P5-REFUND-006
// Scenario:          Refund arrives before its purchase
// Expected Result:   ErrAddonNotFound (retried); no ledger row; the inbox claim rolls back
func TestP5Refund006_BeforePurchase_RetriedNoRow(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)
	req := port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: uuid.New(), Delta: -1_000, BillingRef: uuid.New()}
	eventID := uuid.NewString()

	err := e.deliver(ctx, eventID, req)
	require.ErrorIs(t, err, domain.ErrAddonNotFound)
	n, _ := e.refundRows(t, req.BillingRef)
	assert.Equal(t, 0, n)

	// Not claimed: the redelivery runs again rather than being skipped.
	called := false
	_, err = e.inbox.ProcessOnce(ctx, eventID, "TenantQuotaAddOnChanged", func(context.Context) error { called = true; return nil })
	require.NoError(t, err)
	assert.True(t, called)
}

// Test Case ID:      P5-REFUND-007
// Scenario:          Two refunds together cover the whole purchased quantity (ENT-D16)
// Expected Result:   Status 'refunded' persists (enum value from 000006); total_tokens
//
//	unchanged; remaining 0; ledger sum = total; the pack no longer
//	counts toward the effective balance
func TestP5Refund007_FullRefund_StatusRefundedTotalImmutable(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)
	addonID, purchaseRef := e.grantPack(t, ctx, tenantID, 50_000)

	require.NoError(t, e.deliver(ctx, uuid.NewString(),
		port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -20_000, BillingRef: uuid.New()}))
	require.NoError(t, e.deliver(ctx, uuid.NewString(),
		port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -30_000, BillingRef: uuid.New()}))

	a, err := e.addons.GetByID(ctx, addonID)
	require.NoError(t, err)
	assert.Equal(t, domain.AddonStatusRefunded, a.Status)
	assert.Equal(t, int64(50_000), a.TotalTokens)
	assert.Equal(t, int64(0), a.RemainingTokens)

	sum, err := e.addons.SumRemainingByTenantAndKey(ctx, tenantID, domain.LimitKeyLLMTokenQuotaMonthly)
	require.NoError(t, err)
	assert.Equal(t, int64(0), sum)

	list, err := e.svc.ListAddons(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int64(50_000), list[0].RefundedTokens)
	assert.Equal(t, int64(0), list[0].ConsumedTokens())
	assert.False(t, list[0].Removable())
}

// Test Case ID:      P5-REFUND-008
// Scenario:          Partial refund of an undrawn pack, then operator removal (ENT-O5)
// Expected Result:   total_tokens unchanged after the refund; ListAddons reports the
//
//	refunded quantity from the ledger; the pack is removable
func TestP5Refund008_PartialRefund_UndrawnPackRemovable(t *testing.T) {
	t.Parallel()
	e, tenantID := newRefundEnv(t)
	ctx := withTenant(context.Background(), tenantID)
	addonID, purchaseRef := e.grantPack(t, ctx, tenantID, 50_000)
	require.NoError(t, e.deliver(ctx, uuid.NewString(),
		port.ApplyBillingRefundRequest{TenantID: tenantID, PurchaseRef: purchaseRef, Delta: -10_000, BillingRef: uuid.New()}))

	list, err := e.svc.ListAddons(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int64(50_000), list[0].TotalTokens)
	assert.Equal(t, int64(40_000), list[0].RemainingTokens)
	assert.Equal(t, int64(10_000), list[0].RefundedTokens)
	assert.True(t, list[0].Removable())

	require.NoError(t, e.svc.RemoveAddon(ctx, port.RemoveAddonRequest{
		TenantID: tenantID, AddonID: addonID, RemovedBy: uuid.New(), Reason: "operator correction"}))
	a, err := e.addons.GetByID(ctx, addonID)
	require.NoError(t, err)
	assert.Equal(t, domain.AddonStatusRevoked, a.Status)
	assert.Equal(t, int64(50_000), a.TotalTokens)
}
