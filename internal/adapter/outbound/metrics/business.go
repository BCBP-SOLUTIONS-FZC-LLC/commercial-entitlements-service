package metrics

import (
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// outcome constants for the recompute counter / histogram.
const (
	OutcomeComputed = "computed"
	OutcomeNoop     = "noop"
	OutcomeError    = "error"
)

// IncRecompute increments the recompute counter.
// outcome must be one of OutcomeComputed, OutcomeNoop, OutcomeError.
func IncRecompute(cause domain.EffectiveSetCause, outcome string) {
	if RecomputeTotal != nil {
		RecomputeTotal.WithLabelValues(string(cause), outcome).Inc()
	}
}

// ObserveRecomputeDuration records the duration of one Recompute call.
func ObserveRecomputeDuration(cause domain.EffectiveSetCause, outcome string, d time.Duration) {
	if RecomputeDuration != nil {
		RecomputeDuration.WithLabelValues(string(cause), outcome).Observe(d.Seconds())
	}
}

// IncOverrideSet increments the override-set counter for a limit key.
func IncOverrideSet(key domain.LimitKey) {
	if OverrideSetTotal != nil {
		OverrideSetTotal.WithLabelValues(string(key)).Inc()
	}
}

// IncOverrideRemove increments the override-remove counter for a limit key.
func IncOverrideRemove(key domain.LimitKey) {
	if OverrideRemoveTotal != nil {
		OverrideRemoveTotal.WithLabelValues(string(key)).Inc()
	}
}

// IncOverrideExpire increments the override-expire counter (reconciler sweep).
func IncOverrideExpire() {
	if OverrideExpireTotal != nil {
		OverrideExpireTotal.Inc()
	}
}

// IncAddonGrant increments the addon-grant counter for a limit key.
func IncAddonGrant(key domain.LimitKey) {
	if AddonGrantTotal != nil {
		AddonGrantTotal.WithLabelValues(string(key)).Inc()
	}
}

// IncAddonExpire increments the addon-expire counter (reconciler sweep).
func IncAddonExpire() {
	if AddonExpireTotal != nil {
		AddonExpireTotal.Inc()
	}
}

// IncDrawdown increments the drawdown counter for a limit key.
// hadShortfall indicates whether a quota shortfall occurred.
func IncDrawdown(key domain.LimitKey, hadShortfall bool) {
	if DrawdownTotal != nil {
		sf := "no"
		if hadShortfall {
			sf = "yes"
		}
		DrawdownTotal.WithLabelValues(string(key), sf).Inc()
	}
}

// IncShortfallRaised increments the shortfall-raised counter.
func IncShortfallRaised(key domain.LimitKey, status domain.ShortfallStatus) {
	if ShortfallRaisedTotal != nil {
		ShortfallRaisedTotal.WithLabelValues(string(key), string(status)).Inc()
	}
}

// IncShortfallResolve increments the shortfall-resolved counter.
func IncShortfallResolve(disposition domain.ShortfallDisposition) {
	if ShortfallResolveTotal != nil {
		ShortfallResolveTotal.WithLabelValues(string(disposition)).Inc()
	}
}

// IncCacheHit increments the cache-hit counter for a named resource kind.
// kind should be one of "effective_set", "plan".
func IncCacheHit(kind string) {
	if CacheHitTotal != nil {
		CacheHitTotal.WithLabelValues(kind).Inc()
	}
}

// IncCacheMiss increments the cache-miss counter for a named resource kind.
func IncCacheMiss(kind string) {
	if CacheMissTotal != nil {
		CacheMissTotal.WithLabelValues(kind).Inc()
	}
}
