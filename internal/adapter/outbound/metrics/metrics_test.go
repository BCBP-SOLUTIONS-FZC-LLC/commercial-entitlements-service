package metrics

import (
	"sync"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

var testRegisterOnce sync.Once

// ensureRegistered registers metrics on a fresh prometheus registry so tests
// bypass the gincommon domain vocabulary restriction.
func ensureRegistered(t testing.TB) {
	t.Helper()
	testRegisterOnce.Do(func() {
		register(prometheus.NewRegistry())
	})
}

// ── Register ──────────────────────────────────────────────────────────────

func TestRegister_PopulatesAllVars(t *testing.T) {
	ensureRegistered(t)
	assert.NotNil(t, RecomputeTotal)
	assert.NotNil(t, RecomputeDuration)
	assert.NotNil(t, OverrideSetTotal)
	assert.NotNil(t, OverrideRemoveTotal)
	assert.NotNil(t, OverrideExpireTotal)
	assert.NotNil(t, AddonGrantTotal)
	assert.NotNil(t, AddonExpireTotal)
	assert.NotNil(t, DrawdownTotal)
	assert.NotNil(t, ShortfallRaisedTotal)
	assert.NotNil(t, ShortfallResolveTotal)
	assert.NotNil(t, CacheHitTotal)
	assert.NotNil(t, CacheMissTotal)
	assert.NotNil(t, DuplicateTotal)
	assert.NotNil(t, UnknownEventAcknowledged)
}

func TestRegister_Idempotent(t *testing.T) {
	ensureRegistered(t)
	// Second call must not panic (sync.Once guard)
	assert.NotPanics(t, func() { Register("test") })
}

// ── Inc* guard: nil metrics don't panic ───────────────────────────────────

func TestIncRecompute_NilGuard(t *testing.T) {
	// Temporarily nil to test guard
	saved := RecomputeTotal
	RecomputeTotal = nil
	assert.NotPanics(t, func() { IncRecompute(domain.CauseAddonChange, OutcomeComputed) })
	RecomputeTotal = saved
}

func TestObserveRecomputeDuration_NilGuard(t *testing.T) {
	saved := RecomputeDuration
	RecomputeDuration = nil
	assert.NotPanics(t, func() { ObserveRecomputeDuration(domain.CauseAddonChange, OutcomeNoop, 0) })
	RecomputeDuration = saved
}

func TestIncOverrideSet_NilGuard(t *testing.T) {
	saved := OverrideSetTotal
	OverrideSetTotal = nil
	assert.NotPanics(t, func() { IncOverrideSet(domain.LimitKeyTenderLimit) })
	OverrideSetTotal = saved
}

func TestIncOverrideRemove_NilGuard(t *testing.T) {
	saved := OverrideRemoveTotal
	OverrideRemoveTotal = nil
	assert.NotPanics(t, func() { IncOverrideRemove(domain.LimitKeyTenderLimit) })
	OverrideRemoveTotal = saved
}

func TestIncOverrideExpire_NilGuard(t *testing.T) {
	saved := OverrideExpireTotal
	OverrideExpireTotal = nil
	assert.NotPanics(t, func() { IncOverrideExpire() })
	OverrideExpireTotal = saved
}

func TestIncAddonGrant_NilGuard(t *testing.T) {
	saved := AddonGrantTotal
	AddonGrantTotal = nil
	assert.NotPanics(t, func() { IncAddonGrant(domain.LimitKeyTenderLimit) })
	AddonGrantTotal = saved
}

func TestIncAddonExpire_NilGuard(t *testing.T) {
	saved := AddonExpireTotal
	AddonExpireTotal = nil
	assert.NotPanics(t, func() { IncAddonExpire() })
	AddonExpireTotal = saved
}

func TestIncDrawdown_NilGuard(t *testing.T) {
	saved := DrawdownTotal
	DrawdownTotal = nil
	assert.NotPanics(t, func() { IncDrawdown(domain.LimitKeyTenderLimit, false) })
	DrawdownTotal = saved
}

func TestIncShortfallRaised_NilGuard(t *testing.T) {
	saved := ShortfallRaisedTotal
	ShortfallRaisedTotal = nil
	assert.NotPanics(t, func() { IncShortfallRaised(domain.LimitKeyTenderLimit, domain.ShortfallStatusPendingApproval) })
	ShortfallRaisedTotal = saved
}

func TestIncShortfallResolve_NilGuard(t *testing.T) {
	saved := ShortfallResolveTotal
	ShortfallResolveTotal = nil
	assert.NotPanics(t, func() { IncShortfallResolve(domain.DispositionApprove) })
	ShortfallResolveTotal = saved
}

func TestIncCacheHit_NilGuard(t *testing.T) {
	saved := CacheHitTotal
	CacheHitTotal = nil
	assert.NotPanics(t, func() { IncCacheHit("plan") })
	CacheHitTotal = saved
}

func TestIncCacheMiss_NilGuard(t *testing.T) {
	saved := CacheMissTotal
	CacheMissTotal = nil
	assert.NotPanics(t, func() { IncCacheMiss("effective_set") })
	CacheMissTotal = saved
}

func TestIncDuplicate_NilGuard(t *testing.T) {
	saved := DuplicateTotal
	DuplicateTotal = nil
	assert.NotPanics(t, func() { IncDuplicate("billing-q", "TenantPlanChanged") })
	DuplicateTotal = saved
}

// ── Inc* with registered metrics ─────────────────────────────────────────

func TestIncRecompute_Registered(t *testing.T) {
	ensureRegistered(t)
	assert.NotPanics(t, func() { IncRecompute(domain.CauseAddonChange, OutcomeComputed) })
	assert.NotPanics(t, func() { IncRecompute(domain.CausePlanChange, OutcomeNoop) })
	assert.NotPanics(t, func() { IncRecompute(domain.CauseOverrideChange, OutcomeError) })
}

func TestIncDrawdown_HadShortfall(t *testing.T) {
	ensureRegistered(t)
	assert.NotPanics(t, func() { IncDrawdown(domain.LimitKeyTenderLimit, true) })
	assert.NotPanics(t, func() { IncDrawdown(domain.LimitKeyTenderLimit, false) })
}

func TestIncShortfallRaised_Registered(t *testing.T) {
	ensureRegistered(t)
	assert.NotPanics(t, func() { IncShortfallRaised(domain.LimitKeyTenderLimit, domain.ShortfallStatusAutoAccepted) })
	assert.NotPanics(t, func() { IncShortfallRaised(domain.LimitKeyTenderLimit, domain.ShortfallStatusPendingApproval) })
}
