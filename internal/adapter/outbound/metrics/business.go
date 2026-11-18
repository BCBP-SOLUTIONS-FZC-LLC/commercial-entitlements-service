package metrics

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// IncRecompute increments the recompute counter for the given cause.
func IncRecompute(cause domain.EffectiveSetCause) {
	if RecomputeTotal != nil {
		RecomputeTotal.WithLabelValues(string(cause)).Inc()
	}
}

// IncRecomputeNoop increments the noop guard counter (ENT-INV-6).
func IncRecomputeNoop(cause domain.EffectiveSetCause) {
	if RecomputeNoopTotal != nil {
		RecomputeNoopTotal.WithLabelValues(string(cause)).Inc()
	}
}

// IncOverrideSet increments the override-set counter.
func IncOverrideSet(limitKey domain.LimitKey) {
	if OverrideSetTotal != nil {
		OverrideSetTotal.WithLabelValues(string(limitKey)).Inc()
	}
}

// IncOverrideRemove increments the override-remove counter.
func IncOverrideRemove(limitKey domain.LimitKey) {
	if OverrideRemoveTotal != nil {
		OverrideRemoveTotal.WithLabelValues(string(limitKey)).Inc()
	}
}

// IncOverrideExpire increments the override-expire counter.
func IncOverrideExpire() {
	if OverrideExpireTotal != nil {
		OverrideExpireTotal.Inc()
	}
}

// IncAddonGrant increments the addon-grant counter.
func IncAddonGrant(limitKey domain.LimitKey) {
	if AddonGrantTotal != nil {
		AddonGrantTotal.WithLabelValues(string(limitKey)).Inc()
	}
}

// IncAddonExpire increments the addon-expire counter.
func IncAddonExpire() {
	if AddonExpireTotal != nil {
		AddonExpireTotal.Inc()
	}
}

// IncDrawdown increments the drawdown counter.
func IncDrawdown(limitKey domain.LimitKey, hadShortfall bool) {
	if DrawdownTotal != nil {
		sf := "false"
		if hadShortfall {
			sf = "true"
		}
		DrawdownTotal.WithLabelValues(string(limitKey), sf).Inc()
	}
}

// IncShortfallRaised increments the shortfall-raised counter.
func IncShortfallRaised(limitKey domain.LimitKey, status domain.ShortfallStatus) {
	if ShortfallRaisedTotal != nil {
		ShortfallRaisedTotal.WithLabelValues(string(limitKey), string(status)).Inc()
	}
}

// IncShortfallResolve increments the shortfall-resolve counter.
func IncShortfallResolve(disposition domain.ShortfallDisposition) {
	if ShortfallResolveTotal != nil {
		ShortfallResolveTotal.WithLabelValues(string(disposition)).Inc()
	}
}

// IncCacheHit increments the cache-hit counter.
func IncCacheHit(kind string) {
	if CacheHitTotal != nil {
		CacheHitTotal.WithLabelValues(kind).Inc()
	}
}

// IncCacheMiss increments the cache-miss counter.
func IncCacheMiss(kind string) {
	if CacheMissTotal != nil {
		CacheMissTotal.WithLabelValues(kind).Inc()
	}
}

// RecordDuplicate is called by consumer dedup to increment the duplicate counter.
func RecordDuplicate(_ context.Context, consumer, eventType string) {
	IncDuplicate(consumer, eventType)
}
