// Package metrics registers business-level Prometheus metrics for the
// Commercial Entitlements service.
//
// # Metric Tier Classification (Enterprise Platform Observability Standard)
//
// Tier 1 — Platform Shared (platform_*):
//
//	Emitted automatically by the platform-events library when SQS consumers
//	run (platform_messages_received_total, platform_messages_processed_total,
//	platform_messages_failed_total, platform_retry_total,
//	platform_duplicate_messages_total, etc.).  This service does NOT define
//	these; they are registered via events.InitMetrics() in cmd/*/main.go.
//
// Tier 2 — Domain Shared (commercial_*):
//
//	No cross-service commercial metrics are defined yet.
//
// Tier 3 — Service-Specific (commercial_entitlements_*):
//
//	All metrics in this file. Pattern: <domain>_<service>_<metric>.
//	Labels are injected via gincommon.MetricsRegisterer() (a
//	prometheus.WrapRegistererWith wrapper that adds the const labels
//	service, domain, and environment to every sample — Rule 8 of the
//	naming rules).
//
// # Label Vocabulary (Tier 3)
//
//	cause       : domain.EffectiveSetCause string values
//	outcome     : "computed" | "noop" | "error"
//	limit_key   : bounded set of domain.LimitKey values
//	disposition : domain.ShortfallDisposition string values
//	status      : domain.ShortfallStatus string values (auto_accepted | pending_approval)
//	shortfall   : "yes" | "no"
//	kind        : "effective_set" | "plan"
//	consumer    : stable SQS consumer queue-name string
//	event_type  : platform event type string
//
// High-cardinality identifiers (tenant_id, user_id, request_id, event_id,
// session_id) are explicitly prohibited by the standard and are absent here.
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

	// ── Tier 3: Recompute ─────────────────────────────────────────────────
	// Labels: cause, outcome
	// outcome ∈ {"computed", "noop", "error"}
	// "noop" reflects ENT-INV-6: effective set unchanged, write skipped.
	RecomputeTotal    *prometheus.CounterVec
	RecomputeDuration *prometheus.HistogramVec

	// ── Tier 3: Override operations ───────────────────────────────────────
	// Labels: limit_key
	OverrideSetTotal    *prometheus.CounterVec
	OverrideRemoveTotal *prometheus.CounterVec
	// No labels — overrides are not keyed by limit_key at reconciler sweep time.
	OverrideExpireTotal prometheus.Counter

	// ── Tier 3: Addon / drawdown / shortfall ──────────────────────────────
	// AddonGrantTotal:       limit_key
	// DrawdownTotal:         limit_key, shortfall ∈ {"yes","no"}
	// ShortfallRaisedTotal:  limit_key, status
	// ShortfallResolveTotal: disposition
	AddonGrantTotal       *prometheus.CounterVec
	AddonExpireTotal      prometheus.Counter
	DrawdownTotal         *prometheus.CounterVec
	ShortfallRaisedTotal  *prometheus.CounterVec
	ShortfallResolveTotal *prometheus.CounterVec

	// ── Tier 3: Advisory Valkey cache ─────────────────────────────────────
	// kind ∈ {"effective_set", "plan"}
	CacheHitTotal  *prometheus.CounterVec
	CacheMissTotal *prometheus.CounterVec

	// ── Tier 3: Consumer dedup (service-specific signal) ──────────────────
	// Name aligns with the platform naming convention for
	// platform_duplicate_messages_total (Tier 1, Registry-Proposed) but is
	// scoped to this service and carries a consumer label for per-queue
	// visibility that the platform metric does not expose at this granularity.
	DuplicateTotal *prometheus.CounterVec

	// ── Tier 3: Forward-compat unknown events ─────────────────────────────
	UnknownEventAcknowledged *prometheus.CounterVec
)

// Register initialises all Tier 3 metrics on gincommon's registerer (which
// wraps prometheus.DefaultRegisterer with ConstLabels for service, domain,
// and environment — Rule 8). It is a no-op on subsequent calls so both
// server and reconciler binaries may call it safely.
//
// appEnv is accepted for caller compatibility; the environment label is
// already injected centrally by gincommon.MetricsRegisterer().
func Register(_ string) {
	once.Do(func() {
		register(gincommon.MetricsRegisterer())
	})
}

func register(reg prometheus.Registerer) {
	// Recompute: single counter with cause + outcome labels (instead of two
	// separate counters) so rate(recompute_total[5m]) yields the full picture
	// and noop/error/computed are individually filterable without joining.
	RecomputeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "recompute_total",
		Help: "Total entitlement recomputations, by cause and outcome " +
			"(computed=new snapshot written, noop=ENT-INV-6 skipped, error=failed).",
	}, []string{"cause", "outcome"})

	// Buckets calibrated to a service that reads from IAM over HTTP, Postgres
	// (3-5 queries) and Valkey: p50≈50ms, p99≈500ms, tail up to 5s.
	RecomputeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "recompute_duration_seconds",
		Help:      "Wall-clock time for one Recompute call, by cause and outcome.",
		Buckets:   []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"cause", "outcome"})

	OverrideSetTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "override_set_total",
		Help:      "Override set (create/update) operations, by limit_key.",
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
		Help: "Quota drawdown requests recorded, by limit_key and whether a " +
			"shortfall occurred (shortfall=yes|no).",
	}, []string{"limit_key", "shortfall"})

	ShortfallRaisedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "shortfall_raised_total",
		Help: "Quota shortfalls raised, by limit_key and status " +
			"(auto_accepted|pending_approval).",
	}, []string{"limit_key", "status"})

	ShortfallResolveTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "shortfall_resolve_total",
		Help:      "Shortfalls resolved by operators, by disposition (approve|reject).",
	}, []string{"disposition"})

	CacheHitTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "cache_hit_total",
		Help:      "Valkey advisory cache hits, by kind (effective_set|plan).",
	}, []string{"kind"})

	CacheMissTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "cache_miss_total",
		Help:      "Valkey advisory cache misses (fell through to Postgres/IAM), by kind.",
	}, []string{"kind"})

	// Name aligns with platform_duplicate_messages_total (Tier 1,
	// Registry-Proposed) so dashboards and operators share vocabulary, while
	// the consumer label enables per-queue drill-down not available on the
	// platform metric.
	DuplicateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "duplicate_messages_total",
		Help: "Duplicate events detected and skipped by the inbox dedup layer, " +
			"by consumer and event_type. Complements platform_duplicate_messages_total " +
			"(Tier 1) with per-consumer granularity.",
	}, []string{"consumer", "event_type"})

	UnknownEventAcknowledged = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "commercial",
		Subsystem: "entitlements",
		Name:      "unknown_event_acknowledged_total",
		Help: "Events with no registered handler acknowledged for forward " +
			"compatibility (safe no-op), by event_type.",
	}, []string{"event_type"})

	// MustRegister panics on name collisions so CI surfaces typos early.
	for _, c := range []prometheus.Collector{
		RecomputeTotal, RecomputeDuration,
		OverrideSetTotal, OverrideRemoveTotal, OverrideExpireTotal,
		AddonGrantTotal, AddonExpireTotal,
		DrawdownTotal, ShortfallRaisedTotal, ShortfallResolveTotal,
		CacheHitTotal, CacheMissTotal,
		DuplicateTotal, UnknownEventAcknowledged,
	} {
		reg.MustRegister(c)
	}
}

// IncDuplicate increments the service-specific duplicate counter.
func IncDuplicate(consumer, eventType string) {
	if DuplicateTotal != nil {
		DuplicateTotal.WithLabelValues(consumer, eventType).Inc()
	}
}
