// Package metrics registers business-level Prometheus metrics for the
// Commercial Entitlements service. All collectors are registered on
// gincommon.MetricsRegisterer() so they appear in the shared /metrics scrape.
package metrics

import (
	"sync"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/prometheus/client_golang/prometheus"
)

// Metric instances — each registration is guarded by Once so Register is
// idempotent and safe to call from both cmd/server/main.go and
// cmd/reconciler/main.go.
var (
	once sync.Once

	// Recompute counters (ENT-INV-6 no-op guard).
	RecomputeTotal       *prometheus.CounterVec
	RecomputeNoopTotal   *prometheus.CounterVec
	RecomputeDuration    *prometheus.HistogramVec

	// Override counters.
	OverrideSetTotal    *prometheus.CounterVec
	OverrideRemoveTotal *prometheus.CounterVec
	OverrideExpireTotal prometheus.Counter

	// Addon / drawdown / shortfall counters.
	AddonGrantTotal       *prometheus.CounterVec
	AddonExpireTotal      prometheus.Counter
	DrawdownTotal         *prometheus.CounterVec
	ShortfallRaisedTotal  *prometheus.CounterVec
	ShortfallResolveTotal *prometheus.CounterVec

	// Cache operation counters.
	CacheHitTotal  *prometheus.CounterVec
	CacheMissTotal *prometheus.CounterVec

	// Duplicate-event counter (matches reference pattern).
	DuplicateTotal           *prometheus.CounterVec
	UnknownEventAcknowledged *prometheus.CounterVec
)

// Register initialises all metrics on gincommon's registerer. It is a no-op
// on the second and subsequent calls, so both server and reconciler binaries
// can call it without racing.
func Register(appEnv string) {
	once.Do(func() {
		reg := gincommon.MetricsRegisterer()
		register(reg)
	})
}

// Recorder provides a no-alloc interface for incrementing metrics from job/consumer code.
type Recorder struct{}

func register(reg prometheus.Registerer) {
	RecomputeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "recompute_total",
		Help:      "Total entitlement recomputations, by cause.",
	}, []string{"cause"})

	RecomputeNoopTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "recompute_noop_total",
		Help:      "Recomputations skipped because the effective set was unchanged (ENT-INV-6).",
	}, []string{"cause"})

	RecomputeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "recompute_duration_seconds",
		Help:      "Time taken to recompute an effective set, by cause.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"cause"})

	OverrideSetTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "override_set_total",
		Help:      "Override set operations, by limit_key.",
	}, []string{"limit_key"})

	OverrideRemoveTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "override_remove_total",
		Help:      "Override remove operations, by limit_key.",
	}, []string{"limit_key"})

	OverrideExpireTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "override_expire_total",
		Help:      "Overrides swept by the reconciler expiry job.",
	})

	AddonGrantTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "addon_grant_total",
		Help:      "Quota addon packs granted, by limit_key.",
	}, []string{"limit_key"})

	AddonExpireTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "addon_expire_total",
		Help:      "Addon packs swept by the reconciler addon-expiry job.",
	})

	DrawdownTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "drawdown_total",
		Help:      "Quota drawdown requests recorded, by limit_key.",
	}, []string{"limit_key", "shortfall"})

	ShortfallRaisedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "shortfall_raised_total",
		Help:      "Quota shortfalls raised, by limit_key and status (auto_accepted|pending_approval).",
	}, []string{"limit_key", "status"})

	ShortfallResolveTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "shortfall_resolve_total",
		Help:      "Shortfalls resolved by operators, by disposition.",
	}, []string{"disposition"})

	CacheHitTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "cache_hit_total",
		Help:      "Valkey cache hits, by cache_key_kind.",
	}, []string{"kind"})

	CacheMissTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "cache_miss_total",
		Help:      "Valkey cache misses (fell through to Postgres), by cache_key_kind.",
	}, []string{"kind"})

	DuplicateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "processed_events_duplicates_total",
		Help:      "Duplicate events detected and skipped by the inbox, by consumer and event_type.",
	}, []string{"consumer", "event_type"})

	UnknownEventAcknowledged = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "unknown_event_acknowledged_total",
		Help:      "Events with no registered handler acknowledged for forward compatibility, by event_type.",
	}, []string{"event_type"})

	// Register all; use MustRegister to panic on name collisions so CI
	// surfaces metric name typos.
	for _, c := range []prometheus.Collector{
		RecomputeTotal, RecomputeNoopTotal, RecomputeDuration,
		OverrideSetTotal, OverrideRemoveTotal, OverrideExpireTotal,
		AddonGrantTotal, AddonExpireTotal,
		DrawdownTotal, ShortfallRaisedTotal, ShortfallResolveTotal,
		CacheHitTotal, CacheMissTotal,
		DuplicateTotal, UnknownEventAcknowledged,
	} {
		reg.MustRegister(c)
	}
}

// IncDuplicate increments the duplicate counter.
func IncDuplicate(consumer, eventType string) {
	if DuplicateTotal != nil {
		DuplicateTotal.WithLabelValues(consumer, eventType).Inc()
	}
}
