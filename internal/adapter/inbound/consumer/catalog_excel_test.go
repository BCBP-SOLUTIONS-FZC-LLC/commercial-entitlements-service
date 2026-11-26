// catalog_excel_test.go — test functions matching commercial-entitlements-testing.xlsx
// Sheet: Consumer_Tests, rows CC-01 through CC-08.
package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errTemporalUnavailable = errors.New("temporal: connection refused")

// CC-01: PlanCatalogChanged → Valkey cache refreshed + Temporal fanout workflow started.
func TestCatalogConsumer_PlanCatalogChanged_CacheRefreshed_WorkflowStarted(t *testing.T) {
	c, cat, cache := newCatalogConsumer()
	env := makeCatalogEnv("starter")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Contains(t, cat.refreshCalls, "starter")
	assert.Contains(t, cache.invalidatedPlans, "starter")
}

// CC-02: PlanCatalogChanged when Temporal is unavailable (fanout fails) → NACK (error returned).
func TestCatalogConsumer_TemporalUnavailable_NACKNotAck(t *testing.T) {
	cat := &fakeCatalogSvc{fanoutErr: errTemporalUnavailable}
	cache := &fakeCacheConsumer{}
	cc := NewCatalogConsumer(nil, cat, cache, nopConsumerLogger{})
	env := makeCatalogEnv("pro")
	err := cc.Handle(context.Background(), env)
	assert.Error(t, err, "Temporal unavailable must cause NACK not ack")
}

// CC-03: Duplicate PlanCatalogChanged → no-op via processed_events.
func TestCatalogConsumer_PlanCatalogChanged_Idempotent(t *testing.T) {
	cat := &fakeCatalogSvc{}
	cache := &fakeCacheConsumer{}
	cc := NewCatalogConsumer(&fakeInbox{duplicate: true}, cat, cache, nopConsumerLogger{})
	env := makeCatalogEnv("starter")
	require.NoError(t, cc.Handle(context.Background(), env))
	assert.Empty(t, cat.refreshCalls, "duplicate must be a no-op")
}

// CC-04: Unknown catalog event type → acked without side-effects.
func TestCatalogConsumer_UnknownEventType_AckUnknown(t *testing.T) {
	c, cat, _ := newCatalogConsumer()
	env := makeEnv("cc04", "CatalogFutureEventV2")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, cat.refreshCalls)
}

// CC-05: Cache invalidation failure → non-fatal; refresh still proceeds (CACHE-1).
func TestCatalogConsumer_CacheInvalidationFails_ContinuesRefresh(t *testing.T) {
	cat := &fakeCatalogSvc{}
	cache := &fakeCacheConsumer{invalidatePlanErr: errors.New("valkey timeout")}
	cc := NewCatalogConsumer(nil, cat, cache, nopConsumerLogger{})
	env := makeCatalogEnv("starter")
	require.NoError(t, cc.Handle(context.Background(), env))
	assert.Contains(t, cat.refreshCalls, "starter", "refresh must proceed despite cache error")
}

// CC-06: RefreshPlan failure → error returned → NACK'd (not silently acked).
func TestCatalogConsumer_RefreshPlanFails_NACKd(t *testing.T) {
	cat := &fakeCatalogSvc{refreshErr: errors.New("cat-i2 unavailable")}
	cache := &fakeCacheConsumer{}
	cc := NewCatalogConsumer(nil, cat, cache, nopConsumerLogger{})
	env := makeCatalogEnv("pro")
	err := cc.Handle(context.Background(), env)
	assert.Error(t, err, "refresh failure must be NACK'd")
}

// CC-07: FanoutCatalogRefresh failure → error returned → NACK'd.
func TestCatalogConsumer_FanoutFails_NACKd(t *testing.T) {
	cat := &fakeCatalogSvc{fanoutErr: errors.New("fanout failed")}
	cache := &fakeCacheConsumer{}
	cc := NewCatalogConsumer(nil, cat, cache, nopConsumerLogger{})
	env := makeCatalogEnv("pro")
	err := cc.Handle(context.Background(), env)
	assert.Error(t, err, "fanout failure must be NACK'd")
}

// CC-08: Temporal not wired → noop fanout (fanoutErr=nil) → consumer acks successfully.
func TestCatalogConsumer_TemporalNotWired_NoopFanout_200(t *testing.T) {
	cat := &fakeCatalogSvc{}
	cache := &fakeCacheConsumer{}
	cc := NewCatalogConsumer(nil, cat, cache, nopConsumerLogger{})
	env := makeCatalogEnv("starter")
	require.NoError(t, cc.Handle(context.Background(), env))
	assert.Contains(t, cat.refreshCalls, "starter")
}
