// Package valkey implements the advisory Valkey/Redis cache for the Commercial
// Entitlements service. All operations are fire-and-forget from the service
// layer's perspective: cache errors are returned to callers, but the platform
// contract is that callers MUST NOT fail the business operation on cache errors.
package valkey

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

const (
	effectiveSetTTL = 5 * time.Minute
	planTTL         = 10 * time.Minute
	// ENT-D13: quota ceiling TTL slightly longer than a billing month
	// to tolerate clock skew on the first request of a new period.
	quotaCeilingTTL = 32 * 24 * time.Hour

	// dial/read/write timeouts match reference valkey adapter
	dialTimeout  = 100 * time.Millisecond
	readTimeout  = 50 * time.Millisecond
	writeTimeout = 50 * time.Millisecond
)

// Cache is the Valkey/Redis-backed advisory cache.
type Cache struct {
	rdb *redis.Client
	log port.Logger
}

// New creates a Cache from a Valkey/Redis address or URL.
// address may be "host:port" or a full rediss:// URL.
func New(address string) *Cache {
	rdb := newClient(address)
	return &Cache{rdb: rdb}
}

// WithLogger attaches a logger for cache error diagnostics.
func (c *Cache) WithLogger(l port.Logger) *Cache {
	c.log = l
	return c
}

func (c *Cache) logger() port.Logger {
	if c.log != nil {
		return c.log
	}
	return port.NopLogger{}
}

// newClient builds a redis.Client from an address or URL.
func newClient(address string) *redis.Client {
	if u, err := url.Parse(address); err == nil && (u.Scheme == "redis" || u.Scheme == "rediss") {
		opts, err := redis.ParseURL(address)
		if err == nil {
			opts.DialTimeout = dialTimeout
			opts.ReadTimeout = readTimeout
			opts.WriteTimeout = writeTimeout
			opts.MaxRetries = -1
			opts.MinRetryBackoff = -1
			opts.MaxRetryBackoff = -1
			opts.PoolTimeout = dialTimeout + 10*time.Millisecond
			return redis.NewClient(opts)
		}
	}
	// Plain host:port form.
	return redis.NewClient(&redis.Options{
		Addr:            address,
		DialTimeout:     dialTimeout,
		ReadTimeout:     readTimeout,
		WriteTimeout:    writeTimeout,
		MaxRetries:      -1,
		MinRetryBackoff: -1,
		MaxRetryBackoff: -1,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, network, addr)
		},
	})
}

// Close flushes pending commands and closes the pool.
func (c *Cache) Close() error {
	return c.rdb.Close()
}

// Health pings the Valkey server.
func (c *Cache) Health(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// ── Effective-set cache ───────────────────────────────────────────────────

// GetEffectiveSet returns the cached EffectiveSet for a tenant, if any.
func (c *Cache) GetEffectiveSet(ctx context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error) {
	data, err := c.rdb.Get(ctx, effectiveSetKey(tenantID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var es domain.EffectiveSet
	if err := json.Unmarshal(data, &es); err != nil {
		return nil, err
	}
	return &es, nil
}

// SetEffectiveSet writes an EffectiveSet to cache.
func (c *Cache) SetEffectiveSet(ctx context.Context, tenantID uuid.UUID, es *domain.EffectiveSet) error {
	data, err := json.Marshal(es)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, effectiveSetKey(tenantID), data, effectiveSetTTL).Err()
}

// InvalidateEffectiveSet removes the cached EffectiveSet for a tenant.
func (c *Cache) InvalidateEffectiveSet(ctx context.Context, tenantID uuid.UUID) error {
	return c.rdb.Del(ctx, effectiveSetKey(tenantID)).Err()
}

// ── Plan cache ────────────────────────────────────────────────────────────

// GetPlan returns the cached Plan for a plan code, if any.
func (c *Cache) GetPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	data, err := c.rdb.Get(ctx, planKey(planCode)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan domain.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// SetPlan writes a Plan to cache.
func (c *Cache) SetPlan(ctx context.Context, plan *domain.Plan) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, planKey(plan.Code), data, planTTL).Err()
}

// InvalidatePlan removes the cached Plan for a plan code.
func (c *Cache) InvalidatePlan(ctx context.Context, planCode string) error {
	return c.rdb.Del(ctx, planKey(planCode)).Err()
}

// ── ENT-D13: Quota ceiling & usage counter ────────────────────────────────

// SetQuotaCeiling writes the computed quota ceiling for a tenant+key to Valkey.
// Downstream quota enforcer reads this key on every request (ENT-D13).
// If ceiling is nil (unlimited) the key is deleted so the enforcer treats it as unlimited.
func (c *Cache) SetQuotaCeiling(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey, ceiling *int64) error {
	key := quotaCeilingKey(tenantID, limitKey)
	if ceiling == nil {
		return c.rdb.Del(ctx, key).Err()
	}
	return c.rdb.Set(ctx, key, *ceiling, quotaCeilingTTL).Err()
}

// ResetUsageCounter resets the per-period usage counter to 0 for a tenant+key+period.
// Called by the reconciler (jobs/quota_reset.go) at billing period rollover (ENT-D13).
func (c *Cache) ResetUsageCounter(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey, period string) error {
	key := usageCounterKey(tenantID, limitKey, period)
	return c.rdb.Set(ctx, key, 0, quotaCeilingTTL).Err()
}

// ── Key builders ──────────────────────────────────────────────────────────

func effectiveSetKey(tenantID uuid.UUID) string {
	return fmt.Sprintf("ent:tenant:%s", tenantID)
}

func planKey(planCode string) string {
	return fmt.Sprintf("ent:plans:%s", planCode)
}

// quotaCeilingKey is the ENT-D13 ceiling key read by the downstream quota enforcer.
// Schema: quota:tenant:{tenant_id}:{limit_key}
func quotaCeilingKey(tenantID uuid.UUID, limitKey domain.LimitKey) string {
	return fmt.Sprintf("quota:tenant:%s:%s", tenantID, limitKey)
}

// usageCounterKey is the ENT-D13 per-period usage counter.
// Schema: quota:usage:{tenant_id}:{limit_key}:{period}
// where period is YYYY-MM.
func usageCounterKey(tenantID uuid.UUID, limitKey domain.LimitKey, period string) string {
	return fmt.Sprintf("quota:usage:%s:%s:%s", tenantID, limitKey, period)
}

// Ensure Cache implements port.Cache at compile time.
var _ port.Cache = (*Cache)(nil)

