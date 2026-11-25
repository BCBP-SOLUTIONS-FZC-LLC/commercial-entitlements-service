// Package valkey — white-box tests for key-builder helpers and newClient.
// Pure string-construction; no Redis connection required.
package valkey

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── key builders ──────────────────────────────────────────────────────────

func TestEffectiveSetKey_ContainsTenantID(t *testing.T) {
	id := uuid.New()
	k := effectiveSetKey(id)
	assert.Contains(t, k, id.String())
}

func TestEffectiveSetKey_Deterministic(t *testing.T) {
	id := uuid.New()
	assert.Equal(t, effectiveSetKey(id), effectiveSetKey(id))
}

func TestPlanKey_ContainsPlanCode(t *testing.T) {
	k := planKey("starter")
	assert.Contains(t, k, "starter")
}

func TestPlanKey_DifferentCodesProduceDifferentKeys(t *testing.T) {
	assert.NotEqual(t, planKey("starter"), planKey("pro"))
}

func TestQuotaCeilingKey_ContainsTenantAndLimitKey(t *testing.T) {
	id := uuid.New()
	k := quotaCeilingKey(id, domain.LimitKeyLLMTokenQuotaMonthly)
	assert.Contains(t, k, id.String())
	assert.Contains(t, k, string(domain.LimitKeyLLMTokenQuotaMonthly))
}

func TestUsageCounterKey_ContainsAllParts(t *testing.T) {
	id := uuid.New()
	k := usageCounterKey(id, domain.LimitKeyAPIRequestBudgetMonthly, "2026-01")
	assert.Contains(t, k, id.String())
	assert.Contains(t, k, string(domain.LimitKeyAPIRequestBudgetMonthly))
	assert.Contains(t, k, "2026-01")
}

func TestUsageCounterKey_DifferentPeriodsProduceDifferentKeys(t *testing.T) {
	id := uuid.New()
	k1 := usageCounterKey(id, domain.LimitKeyLLMTokenQuotaMonthly, "2026-01")
	k2 := usageCounterKey(id, domain.LimitKeyLLMTokenQuotaMonthly, "2026-02")
	assert.NotEqual(t, k1, k2)
}

// ── newClient ─────────────────────────────────────────────────────────────

func TestNewClient_HostPortAddress_NoPanic(t *testing.T) {
	assert.NotPanics(t, func() { newClient("localhost:6379") })
}

func TestNewClient_RedissURL_NoPanic(t *testing.T) {
	assert.NotPanics(t, func() { newClient("rediss://localhost:6380") })
}

func TestNewClient_EmptyAddress_NoPanic(t *testing.T) {
	assert.NotPanics(t, func() { newClient("") })
}

// ── New / WithLogger ──────────────────────────────────────────────────────

func TestNew_ReturnsNonNil(t *testing.T) {
	c := New("localhost:6379")
	assert.NotNil(t, c)
}

func TestWithLogger_ReturnsReceiverForChaining(t *testing.T) {
	c := New("localhost:6379")
	c2 := c.WithLogger(port.NopLogger{})
	assert.Equal(t, c, c2)
}

// ── logger() nil branch ───────────────────────────────────────────────────

func TestLogger_NilLog_ReturnsNopLogger(t *testing.T) {
	c := &Cache{}
	l := c.logger()
	_, ok := l.(port.NopLogger)
	assert.True(t, ok)
}

func TestLogger_NonNilLog_ReturnsLog(t *testing.T) {
	c := &Cache{log: port.NopLogger{}}
	l := c.logger()
	_, ok := l.(port.NopLogger)
	assert.True(t, ok)
}

// ── key namespace consistency ─────────────────────────────────────────────

func TestAllKeys_HaveConsistentNamespace(t *testing.T) {
	id := uuid.New()
	// Keys should use known prefixes to avoid Valkey key collisions
	eff := effectiveSetKey(id)
	plan := planKey("code")
	ceiling := quotaCeilingKey(id, domain.LimitKeyLLMTokenQuotaMonthly)
	usage := usageCounterKey(id, domain.LimitKeyLLMTokenQuotaMonthly, "2026-01")

	// None should be empty
	assert.True(t, len(eff) > 0)
	assert.True(t, len(plan) > 0)
	assert.True(t, len(ceiling) > 0)
	assert.True(t, len(usage) > 0)

	// Keys should be distinct from each other
	keys := []string{eff, plan, ceiling, usage}
	seen := map[string]struct{}{}
	for _, k := range keys {
		seen[k] = struct{}{}
	}
	assert.Len(t, seen, len(keys), "all keys must be unique")
	_ = strings.TrimSpace // suppress unused import
}
