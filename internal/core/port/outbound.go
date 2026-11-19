package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── Repository interfaces ──────────────────────────────────────────────────

// SnapshotRepository persists and retrieves entitlement snapshots.
// There is exactly one row per tenant; Upsert replaces it atomically with a
// version guard (optimistic lock).
type SnapshotRepository interface {
	Upsert(ctx context.Context, snap *domain.EntitlementSnapshot) error
	GetLatest(ctx context.Context, tenantID uuid.UUID) (*domain.EntitlementSnapshot, error)
}

// OverrideRepository persists and retrieves operator overrides.
type OverrideRepository interface {
	Upsert(ctx context.Context, o *domain.Override) error
	Delete(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) error
	Get(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) (*domain.Override, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID) ([]domain.Override, error)
	ListExpired(ctx context.Context, before time.Time) ([]domain.Override, error)
	InsertHistory(ctx context.Context, h *domain.OverrideHistory) error
	ListHistory(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) ([]domain.OverrideHistory, error)
}

// AddonRepository persists and retrieves quota addon packs.
type AddonRepository interface {
	Insert(ctx context.Context, a *domain.QuotaAddon) error
	Update(ctx context.Context, a *domain.QuotaAddon) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.QuotaAddon, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID, limitKey *domain.LimitKey) ([]domain.QuotaAddon, error)
	ListExpired(ctx context.Context, before time.Time) ([]domain.QuotaAddon, error)
	ListActiveByTenantAndKey(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddon, error)
	SumRemainingByTenantAndKey(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) (int64, error)
}

// DrawdownRepository persists quota addon drawdown records.
type DrawdownRepository interface {
	Insert(ctx context.Context, d *domain.QuotaAddonDrawdown) error
	GetByRequestID(ctx context.Context, tenantID uuid.UUID, requestID uuid.UUID) (*domain.QuotaAddonDrawdown, error)
	ListByTenantAndKey(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddonDrawdown, error)
}

// ShortfallRepository persists and retrieves shortfall records.
type ShortfallRepository interface {
	Insert(ctx context.Context, s *domain.QuotaAddonShortfall) error
	Update(ctx context.Context, s *domain.QuotaAddonShortfall) error
	GetByDrawdownID(ctx context.Context, drawdownID uuid.UUID) (*domain.QuotaAddonShortfall, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.QuotaAddonShortfall, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID, statusFilter []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error)
	CountRecentForTenant(ctx context.Context, tenantID uuid.UUID, windowDays int) (int, error)
}

// TrialRepository persists and retrieves trial assignments and trial overrides.
type TrialRepository interface {
	UpsertAssignment(ctx context.Context, a *domain.TenantTrialAssignment) error
	GetAssignment(ctx context.Context, tenantID uuid.UUID) (*domain.TenantTrialAssignment, error)
	DeleteAssignment(ctx context.Context, tenantID uuid.UUID) error

	UpsertOverride(ctx context.Context, o *domain.TrialEntitlementOverride) error
	GetOverride(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) (*domain.TrialEntitlementOverride, error)
	ListOverrides(ctx context.Context, tenantID uuid.UUID) ([]domain.TrialEntitlementOverride, error)
	DeleteOverride(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) error
	DeleteAllOverrides(ctx context.Context, tenantID uuid.UUID) error
}

// CatalogCacheRepository caches plan definitions fetched from IAM catalog admin.
type CatalogCacheRepository interface {
	GetPlan(ctx context.Context, planCode string) (*domain.Plan, error)
	SetPlan(ctx context.Context, plan *domain.Plan) error
	InvalidatePlan(ctx context.Context, planCode string) error
}

// ── Outbound client interfaces ─────────────────────────────────────────────

// IAMClient fetches plan and subscription data from the IAM catalog service.
type IAMClient interface {
	FetchPlan(ctx context.Context, planCode string) (*domain.Plan, error)
	FetchTenantSubscription(ctx context.Context, tenantID uuid.UUID) (*TenantSubscription, error)
}

// TenantSubscription is the upstream subscription record for a tenant.
type TenantSubscription struct {
	TenantID      uuid.UUID
	PlanCode      string
	State         string
	TrialProfile  *string
	TrialEndsAt   *time.Time
	RecordVersion int64
}

// TemporalStarter triggers Temporal workflows for async fanout.
type TemporalStarter interface {
	StartCatalogFanout(ctx context.Context, planCode string) error
	StartExpiryFanout(ctx context.Context) error
}

// ── Cache interface ────────────────────────────────────────────────────────

// Cache is the advisory Valkey/Redis cache for the entitlements service.
// All operations fail silently (errors are returned to callers who choose
// to ignore them).
type Cache interface {
	GetEffectiveSet(ctx context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error)
	SetEffectiveSet(ctx context.Context, tenantID uuid.UUID, es *domain.EffectiveSet) error
	InvalidateEffectiveSet(ctx context.Context, tenantID uuid.UUID) error

	// ENT-D13: quota ceiling write for downstream quota enforcer.
	SetQuotaCeiling(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey, ceiling *int64) error
	// ENT-D13: usage counter reset at billing period rollover.
	ResetUsageCounter(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey, period string) error
	// DeleteUsageCounters deletes all quota:usage:*:{limitKey}:{period} keys via
	// SCAN+DEL so the reconciler can flush stale counters without enumerating tenants.
	DeleteUsageCounters(ctx context.Context, limitKey domain.LimitKey, period string) (int64, error)

	GetPlan(ctx context.Context, planCode string) (*domain.Plan, error)
	SetPlan(ctx context.Context, plan *domain.Plan) error
	InvalidatePlan(ctx context.Context, planCode string) error

	Health(ctx context.Context) error
	Close() error
}

// ── TxRunner ──────────────────────────────────────────────────────────────

// TxRunner is the seam services use to run business writes and event enqueues
// atomically (EVT-10, CONS-1..4). The concrete implementation opens a
// transaction, attaches it plus event publisher and audit recorder to ctx,
// and commits all three together.
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// ── EventPublisher ────────────────────────────────────────────────────────

// EventPublisher enqueues events into the transactional outbox. The insert
// MUST share the same transaction as the business write so state and event
// commit atomically (EVT-10, CONS-1..4).
//
// Callers never pass a pgx.Tx. TxRunner.RunInTx stores the open transaction
// on ctx; the eventbus adapter reads it via port.TxFromContext.
type EventPublisher interface {
	Enqueue(ctx context.Context, event *domain.DomainEvent) error
}

// ── AuditRecorder ─────────────────────────────────────────────────────────

// AuditActor identifies who performed an auditable action.
type AuditActor struct {
	Type string
	ID   string
}

// AuditTarget is the resource that was acted upon.
type AuditTarget struct {
	Type string
	ID   string
}

// AuditEntry describes a single auditable action. Field names follow the
// platform-audit schema so toEntry in the adapter can copy without renaming.
type AuditEntry struct {
	TenantID        *uuid.UUID
	EntryType       string
	Action          string
	Actor           AuditActor
	OccurredAt      time.Time
	SourceEventType string
	IPAddress       string
	UserAgent       string
	Target          *AuditTarget
	Metadata        map[string]any
	IdempotencyKey  string
}

// AuditRecorder enqueues audit entries into the platform-audit outbox inside
// the same transaction as the business write.
type AuditRecorder interface {
	Record(ctx context.Context, entry AuditEntry) error
	RecordBatch(ctx context.Context, entries []AuditEntry) error
}

// ── Inbox (processed_events dedup) ────────────────────────────────────────

// Inbox provides at-most-once delivery semantics using a processed_events table.
// The claim, projection writes, and business fn all commit in one transaction.
type Inbox interface {
	ProcessOnce(ctx context.Context, consumer, eventID, eventType string, fn func(txCtx context.Context) error) (duplicate bool, err error)
}

// IdempotencyRepository combines Inbox with pruning for the reconciler job.
type IdempotencyRepository interface {
	Inbox
	Prune(ctx context.Context, olderThan time.Duration, batchLimit int) (int64, error)
}
