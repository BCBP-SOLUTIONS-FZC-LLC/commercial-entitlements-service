package metrics

import (
	"sync"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

var testRegisterOnce sync.Once

// testRegisterAll creates all package-level metric vars using raw prometheus
// constructors and registers them on reg. This keeps test isolation without
// requiring gincommon identity initialisation (which is not available in unit
// tests). Production always goes through gincommon.New* via register().
func testRegisterAll(reg *prometheus.Registry) {
	RecomputeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_recompute_total",
		Help: "Total entitlement recomputations, by cause and outcome.",
	}, []string{"cause", "outcome"})
	RecomputeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "commercial_entitlements_recompute_duration_seconds",
		Help:    "Wall-clock time for one Recompute call, by cause and outcome.",
		Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"cause", "outcome"})
	OverrideSetTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_override_set_total",
		Help: "Override set (create/update) operations, by limit_key.",
	}, []string{"limit_key"})
	OverrideRemoveTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_override_remove_total",
		Help: "Override remove operations, by limit_key.",
	}, []string{"limit_key"})
	OverrideExpireTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "commercial_entitlements_override_expire_total",
		Help: "Overrides swept by the reconciler expiry job.",
	})
	AddonGrantTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_addon_grant_total",
		Help: "Quota addon packs granted, by limit_key.",
	}, []string{"limit_key"})
	AddonRemoveTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "commercial_entitlements_addon_remove_total",
		Help: "Quota addon packs removed by operator (ENT-O5 DELETE).",
	})
	AddonExpireTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "commercial_entitlements_addon_expire_total",
		Help: "Addon packs swept by the reconciler addon-expiry job.",
	})
	DrawdownTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_drawdown_total",
		Help: "Quota drawdown requests recorded, by limit_key and shortfall.",
	}, []string{"limit_key", "shortfall"})
	ShortfallRaisedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_shortfall_raised_total",
		Help: "Quota shortfalls raised, by limit_key and status.",
	}, []string{"limit_key", "status"})
	ShortfallResolveTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_shortfall_resolve_total",
		Help: "Shortfalls resolved by operators, by disposition.",
	}, []string{"disposition"})
	CacheHitTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_cache_hit_total",
		Help: "Valkey advisory cache hits, by kind.",
	}, []string{"kind"})
	CacheMissTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_cache_miss_total",
		Help: "Valkey advisory cache misses, by kind.",
	}, []string{"kind"})
	TrialAssignmentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_trial_assignment_total",
		Help: "Trial profile assignments or updates, by profile.",
	}, []string{"profile"})
	DuplicateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_duplicate_messages_total",
		Help: "Duplicate events detected and skipped by the inbox dedup layer.",
	}, []string{"consumer", "event_type"})
	UnknownEventAcknowledged = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_unknown_event_acknowledged_total",
		Help: "Events with no registered handler acknowledged for forward compatibility.",
	}, []string{"consumer", "event_type"})
	ConsumedEventRejectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_consumed_event_rejected_total",
		Help: "Inbound SQS events rejected before reaching the domain handler.",
	}, []string{"consumer", "event_type", "reason"})
	RLSViolationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commercial_entitlements_rls_violations_total",
		Help: "PostgreSQL row-level security policy violations.",
	}, []string{"violation_type"})

	reg.MustRegister(
		RecomputeTotal, RecomputeDuration,
		OverrideSetTotal, OverrideRemoveTotal, OverrideExpireTotal,
		AddonGrantTotal, AddonRemoveTotal, AddonExpireTotal,
		DrawdownTotal, ShortfallRaisedTotal, ShortfallResolveTotal,
		CacheHitTotal, CacheMissTotal,
		TrialAssignmentTotal,
		DuplicateTotal, UnknownEventAcknowledged,
		ConsumedEventRejectedTotal, RLSViolationsTotal,
	)
}

// ensureRegistered registers metrics on a fresh prometheus registry so tests
// are isolated from prometheus.DefaultRegisterer and do not require gincommon
// initialisation. The prometheus.NewCounterVec construction path is used for
// test isolation; production always goes through gincommon.MetricsRegisterer().
//
// It also burns the package-level once so that any subsequent call to
// Register() (e.g. TestRegister_Idempotent) is a true no-op and cannot reach
// the gincommon.New* constructors, which require an initialised identity.
func ensureRegistered(t testing.TB) {
	t.Helper()
	testRegisterOnce.Do(func() {
		testRegisterAll(prometheus.NewRegistry())
		// Drain the production once so Register() is idempotent in tests
		// without triggering gincommon.New* (which needs ObservabilityMiddlewares).
		once.Do(func() {})
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
	assert.NotNil(t, AddonRemoveTotal)
	assert.NotNil(t, AddonExpireTotal)
	assert.NotNil(t, DrawdownTotal)
	assert.NotNil(t, ShortfallRaisedTotal)
	assert.NotNil(t, ShortfallResolveTotal)
	assert.NotNil(t, CacheHitTotal)
	assert.NotNil(t, CacheMissTotal)
	assert.NotNil(t, TrialAssignmentTotal)
	assert.NotNil(t, DuplicateTotal)
	assert.NotNil(t, UnknownEventAcknowledged)
	assert.NotNil(t, ConsumedEventRejectedTotal)
	assert.NotNil(t, RLSViolationsTotal)
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

func TestIncAddonRemove_NilGuard(t *testing.T) {
	saved := AddonRemoveTotal
	AddonRemoveTotal = nil
	assert.NotPanics(t, func() { IncAddonRemove() })
	AddonRemoveTotal = saved
}

func TestIncTrialAssignment_NilGuard(t *testing.T) {
	saved := TrialAssignmentTotal
	TrialAssignmentTotal = nil
	assert.NotPanics(t, func() { IncTrialAssignment("trial_specific") })
	TrialAssignmentTotal = saved
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

func TestIncUnknownEventAcknowledged_NilGuard(t *testing.T) {
	saved := UnknownEventAcknowledged
	UnknownEventAcknowledged = nil
	assert.NotPanics(t, func() { IncUnknownEventAcknowledged("billing-q", "TenantPlanChanged") })
	UnknownEventAcknowledged = saved
}

func TestIncConsumedEventRejected_NilGuard(t *testing.T) {
	saved := ConsumedEventRejectedTotal
	ConsumedEventRejectedTotal = nil
	assert.NotPanics(t, func() { IncConsumedEventRejected("billing-entitlements-q", "TenantPlanChanged", "schema_violation") })
	ConsumedEventRejectedTotal = saved
}

func TestIncRLSViolation_NilGuard(t *testing.T) {
	saved := RLSViolationsTotal
	RLSViolationsTotal = nil
	assert.NotPanics(t, func() { IncRLSViolation("cross_tenant_access") })
	RLSViolationsTotal = saved
}

// ── Inc* with registered metrics ─────────────────────────────────────────

func TestIncUnknownEventAcknowledged_Registered(t *testing.T) {
	ensureRegistered(t)
	assert.NotPanics(t, func() { IncUnknownEventAcknowledged("billing-q", "TenantPlanChanged") })
	assert.NotPanics(t, func() { IncUnknownEventAcknowledged("catalog-q", "unknown") })
}

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

// ── Registered tests for all remaining Inc* helpers ───────────────────────
// These cover the non-nil branch (the actual metric increment) for every
// helper that only had a nil-guard test.

func TestAllRemainingInc_Registered(t *testing.T) {
	ensureRegistered(t)
	assert.NotPanics(t, func() { ObserveRecomputeDuration(domain.CauseAddonChange, OutcomeComputed, 0) })
	assert.NotPanics(t, func() { IncOverrideSet(domain.LimitKeyTenderLimit) })
	assert.NotPanics(t, func() { IncOverrideRemove(domain.LimitKeyTenderLimit) })
	assert.NotPanics(t, func() { IncOverrideExpire() })
	assert.NotPanics(t, func() { IncAddonGrant(domain.LimitKeyLLMTokenQuotaMonthly) })
	assert.NotPanics(t, func() { IncAddonRemove() })
	assert.NotPanics(t, func() { IncAddonExpire() })
	assert.NotPanics(t, func() { IncTrialAssignment("trial_specific") })
	assert.NotPanics(t, func() { IncShortfallResolve(domain.DispositionApprove) })
	assert.NotPanics(t, func() { IncShortfallResolve(domain.DispositionReject) })
	assert.NotPanics(t, func() { IncCacheHit("effective_set") })
	assert.NotPanics(t, func() { IncCacheHit("plan") })
	assert.NotPanics(t, func() { IncCacheMiss("effective_set") })
	assert.NotPanics(t, func() { IncCacheMiss("plan") })
	assert.NotPanics(t, func() { IncDuplicate("billing-q", "TenantPlanChanged") })
	assert.NotPanics(t, func() { IncDuplicate("catalog-q", "unknown") })
	assert.NotPanics(t, func() { IncConsumedEventRejected("billing-entitlements-q", "TenantPlanChanged", "schema_violation") })
	assert.NotPanics(t, func() { IncConsumedEventRejected("catalog-entitlements-q", "unknown", "schema_violation") })
	assert.NotPanics(t, func() { IncRLSViolation("cross_tenant_access") })
}
