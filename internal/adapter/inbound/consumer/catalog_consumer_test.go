package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCatalogConsumer() (*CatalogConsumer, *fakeCatalogSvc, *fakeCacheConsumer) {
	cat := &fakeCatalogSvc{}
	cache := &fakeCacheConsumer{}
	c := NewCatalogConsumer(nil, cat, cache, nopConsumerLogger{})
	return c, cat, cache
}

func makeCatalogEnv(planCode string) events.Envelope[json.RawMessage] {
	// Field name "code" matches iam-catalog-admin's planCatalogChangedData wire shape.
	raw, _ := json.Marshal(map[string]any{"code": planCode, "change_type": "updated"})
	return events.Envelope[json.RawMessage]{ID: "cat-1", Type: "PlanCatalogChanged", Payload: raw}
}

// ── PlanCatalogChanged ────────────────────────────────────────────────────

func TestCatalogConsumer_PlanCatalogChanged_InvalidatesCacheAndRefreshes(t *testing.T) {
	c, cat, cache := newCatalogConsumer()
	env := makeCatalogEnv("starter")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Contains(t, cache.invalidatedPlans, "starter")
	assert.Contains(t, cat.refreshCalls, "starter")
}

func TestCatalogConsumer_PlanCatalogChanged_MalformedPayload_Acks(t *testing.T) {
	c, cat, _ := newCatalogConsumer()
	env := events.Envelope[json.RawMessage]{ID: "c2", Type: "PlanCatalogChanged", Payload: json.RawMessage(`not-json`)}
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, cat.refreshCalls, "malformed payload is acked without refresh")
}

func TestCatalogConsumer_PlanCatalogChanged_CacheInvalidateError_NonFatal(t *testing.T) {
	cat := &fakeCatalogSvc{}
	cache := &fakeCacheConsumer{invalidatePlanErr: errors.New("cache down")}
	c := NewCatalogConsumer(nil, cat, cache, nopConsumerLogger{})
	env := makeCatalogEnv("pro")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Contains(t, cat.refreshCalls, "pro", "refresh must still happen when cache invalidation fails")
}

func TestCatalogConsumer_PlanCatalogChanged_NilCache_NoError(t *testing.T) {
	cat := &fakeCatalogSvc{}
	c := NewCatalogConsumer(nil, cat, nil, nopConsumerLogger{})
	env := makeCatalogEnv("enterprise")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Contains(t, cat.refreshCalls, "enterprise")
}

func TestCatalogConsumer_PlanCatalogChanged_RefreshError_Propagates(t *testing.T) {
	cat := &fakeCatalogSvc{refreshErr: errors.New("iam down")}
	c := NewCatalogConsumer(nil, cat, nil, nopConsumerLogger{})
	env := makeCatalogEnv("starter")
	err := c.Handle(context.Background(), env)
	assert.Error(t, err)
}

func TestCatalogConsumer_PlanCatalogChanged_FanoutError_Propagates(t *testing.T) {
	cat := &fakeCatalogSvc{fanoutErr: errors.New("temporal down")}
	c := NewCatalogConsumer(nil, cat, nil, nopConsumerLogger{})
	env := makeCatalogEnv("starter")
	err := c.Handle(context.Background(), env)
	assert.Error(t, err)
}

// ── Unknown event ─────────────────────────────────────────────────────────

func TestCatalogConsumer_UnknownEvent_Acks(t *testing.T) {
	c, _, _ := newCatalogConsumer()
	env := makeEnv("c99", "SomeFutureEvent")
	require.NoError(t, c.Handle(context.Background(), env))
}

// ── With inbox ────────────────────────────────────────────────────────────

func TestCatalogConsumer_WithInbox_Duplicate_SkipsFn(t *testing.T) {
	cat := &fakeCatalogSvc{}
	c := NewCatalogConsumer(&fakeInbox{duplicate: true}, cat, nil, nopConsumerLogger{})
	env := makeCatalogEnv("starter")
	require.NoError(t, c.Handle(context.Background(), env))
	assert.Empty(t, cat.refreshCalls)
}
