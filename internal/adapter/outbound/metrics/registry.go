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
//	naming rules). The registerer is established by calling
//	gincommon.ObservabilityMiddlewares before Register.
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
//	consumer        : stable SQS consumer queue-name string
//	event_type      : platform event type string
//	reason          : rejection reason ("schema_violation")
//	violation_type  : RLS violation category ("cross_tenant_access")
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
	// AddonRemoveTotal:      no labels (addonId → limit_key resolution would need extra DB round-trip)
	// DrawdownTotal:         limit_key, shortfall ∈ {"yes","no"}
	// ShortfallRaisedTotal:  limit_key, status
	// ShortfallResolveTotal: disposition
	AddonGrantTotal       *prometheus.CounterVec
	AddonRemoveTotal      prometheus.Counter
	AddonExpireTotal      prometheus.Counter
	DrawdownTotal         *prometheus.CounterVec
	ShortfallRaisedTotal  *prometheus.CounterVec
	ShortfallResolveTotal *prometheus.CounterVec

	// ── Tier 3: Trial assignment ───────────────────────────────────────────
	// profile ∈ {"trial_specific","reduced_plan","full_plan","custom"}
	TrialAssignmentTotal *prometheus.CounterVec

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

	// ── Tier 3: Consumed-event schema rejection ────────────────────────────
	// Labels: consumer, event_type, reason
	// reason ∈ {"schema_violation"}
	// Emitted in sqspipeline.Handler when the ValidatingCodec rejects a
	// payload before it reaches the domain handler.
	ConsumedEventRejectedTotal *prometheus.CounterVec

	// ── Tier 3: RLS policy violations ─────────────────────────────────────
	// Labels: violation_type ∈ {"cross_tenant_access"}
	// Emitted by the postgres adapter when PostgreSQL returns SQLSTATE 42501
	// with "row-level security" in the message, indicating that RLS blocked
	// a cross-tenant row access. Any non-zero value is a critical compliance
	// signal (see CommercialEntitlementsRLSCrossTenantAccess alert).
	RLSViolationsTotal *prometheus.CounterVec

	// ── Tier 3: Billing add-on conflicts (B-8) ────────────────────────────
	// Labels: event_type ∈ {"TenantQuotaAddOnChanged"}, reason ∈ {"purchase"}.
	// A Billing add-on event whose billing_ref is already held by another
	// tenant's pack; nothing is granted (see CommercialEntitlementsBillingRefConflict).
	BillingRefConflictTotal *prometheus.CounterVec
)

// must panics if err is non-nil, otherwise returns v. Used for metric
// construction where a panic at startup surfaces misconfiguration early
// (same contract as the previous prometheus.MustRegister pattern).
func must[T any](v T, err error) T {
	if err != nil {
		panic("metrics: " + err.Error())
	}
	return v
}

// Register initialises all Tier 3 metrics on gincommon's registerer (which
// wraps prometheus.DefaultRegisterer with ConstLabels for service, domain,
// and environment — Rule 8). It is a no-op on subsequent calls so both
// server and reconciler binaries may call it safely.
//
// Must be called after gincommon.ObservabilityMiddlewares so that the
// registerer wrapper carries the correct const labels.
//
// appEnv is accepted for caller compatibility; the environment label is
// already injected centrally by gincommon.MetricsRegisterer().
func Register(_ string) {
	once.Do(func() {
		register()
	})
}

func register() {
	// Recompute: single counter with cause + outcome labels (instead of two
	// separate counters) so rate(recompute_total[5m]) yields the full picture
	// and noop/error/computed are individually filterable without joining.
	RecomputeTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_recompute_total",
		"Total entitlement recomputations, by cause and outcome "+
			"(computed=new snapshot written, noop=ENT-INV-6 skipped, error=failed).",
		"cause", "outcome"))

	// Buckets calibrated to a service that reads from IAM over HTTP, Postgres
	// (3-5 queries) and Valkey: p50≈50ms, p99≈500ms, tail up to 5s.
	RecomputeDuration = must(gincommon.NewHistogramVec(nil,
		"commercial_entitlements_recompute_duration_seconds",
		"Wall-clock time for one Recompute call, by cause and outcome.",
		[]float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		"cause", "outcome"))

	OverrideSetTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_override_set_total",
		"Override set (create/update) operations, by limit_key.",
		"limit_key"))

	OverrideRemoveTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_override_remove_total",
		"Override remove operations, by limit_key.",
		"limit_key"))

	OverrideExpireTotal = must(gincommon.NewCounter(nil,
		"commercial_entitlements_override_expire_total",
		"Overrides swept by the reconciler expiry job."))

	AddonGrantTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_addon_grant_total",
		"Quota addon packs granted, by limit_key.",
		"limit_key"))

	AddonRemoveTotal = must(gincommon.NewCounter(nil,
		"commercial_entitlements_addon_remove_total",
		"Quota addon packs removed by operator (ENT-O5 DELETE)."))

	AddonExpireTotal = must(gincommon.NewCounter(nil,
		"commercial_entitlements_addon_expire_total",
		"Addon packs swept by the reconciler addon-expiry job."))

	DrawdownTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_drawdown_total",
		"Quota drawdown requests recorded, by limit_key and whether a "+
			"shortfall occurred (shortfall=yes|no).",
		"limit_key", "shortfall"))

	ShortfallRaisedTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_shortfall_raised_total",
		"Quota shortfalls raised, by limit_key and status "+
			"(auto_accepted|pending_approval).",
		"limit_key", "status"))

	ShortfallResolveTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_shortfall_resolve_total",
		"Shortfalls resolved by operators, by disposition (approve|reject).",
		"disposition"))

	CacheHitTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_cache_hit_total",
		"Valkey advisory cache hits, by kind (effective_set|plan).",
		"kind"))

	CacheMissTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_cache_miss_total",
		"Valkey advisory cache misses (fell through to Postgres/IAM), by kind.",
		"kind"))

	// Name aligns with platform_duplicate_messages_total (Tier 1,
	// Registry-Proposed) so dashboards and operators share vocabulary, while
	// the consumer label enables per-queue drill-down not available on the
	// platform metric.
	DuplicateTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_duplicate_messages_total",
		"Duplicate events detected and skipped by the inbox dedup layer, "+
			"by consumer and event_type. Complements platform_duplicate_messages_total "+
			"(Tier 1) with per-consumer granularity.",
		"consumer", "event_type"))

	UnknownEventAcknowledged = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_unknown_event_acknowledged_total",
		"Events with no registered handler acknowledged for forward "+
			"compatibility (safe no-op), by consumer and event_type.",
		"consumer", "event_type"))

	TrialAssignmentTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_trial_assignment_total",
		"Trial profile assignments or updates, by profile.",
		"profile"))

	ConsumedEventRejectedTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_consumed_event_rejected_total",
		"Inbound SQS events rejected before reaching the domain handler, "+
			"by consumer, event_type, and reason. reason=schema_violation means "+
			"the payload failed the ValidatingCodec check and was forwarded to DLQ.",
		"consumer", "event_type", "reason"))

	BillingRefConflictTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_billing_ref_conflict_total",
		"Billing add-on events dropped because their billing_ref is already "+
			"held by another tenant's pack (B-8). Nothing is granted and the "+
			"event is acked. Any non-zero value is a money signal — drives the "+
			"CommercialEntitlementsBillingRefConflict alert.",
		"event_type", "reason"))

	RLSViolationsTotal = must(gincommon.NewCounterVec(nil,
		"commercial_entitlements_rls_violations_total",
		"PostgreSQL row-level security policy violations (SQLSTATE 42501 + "+
			"'row-level security' message) detected by the postgres adapter. "+
			"violation_type=cross_tenant_access. Any non-zero value is a critical "+
			"compliance signal — investigate rls_violation_log immediately.",
		"violation_type"))
}

// IncDuplicate increments the service-specific duplicate counter.
func IncDuplicate(consumer, eventType string) {
	if DuplicateTotal != nil {
		DuplicateTotal.WithLabelValues(consumer, eventType).Inc()
	}
}
