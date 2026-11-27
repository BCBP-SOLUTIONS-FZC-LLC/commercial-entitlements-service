// Package service_test provides shared test fakes for all service-layer unit tests.
package unit_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── helpers ────────────────────────────────────────────────────────────────

func i64ptr(v int64) *int64   { return &v }
func iptr(v int) *int         { return &v }
func strptr(s string) *string { return &s }

func samplePlan(code string) *domain.Plan {
	return &domain.Plan{
		Code:                    code,
		DisplayName:             "Test Plan",
		LLMTokenQuotaMonthly:    i64ptr(1_000_000),
		APIRequestBudgetMonthly: i64ptr(50_000),
		APIRateLimitRPS:         100,
		ModelTier:               "standard",
		FeatureSet:              map[string]interface{}{"feature_a": true, "feature_b": false},
		RecordVersion:           1,
		TrialProfiles:           map[string]domain.TrialProfileLimits{},
	}
}

func sampleSub(tenantID uuid.UUID, planCode, state string) *port.TenantSubscription {
	return &port.TenantSubscription{
		TenantID:      tenantID,
		PlanCode:      planCode,
		State:         state,
		RecordVersion: 1,
	}
}

// ovKey is a composite key for override maps.
type ovKey struct {
	tenantID uuid.UUID
	key      domain.LimitKey
}

// ── fakePublisher ──────────────────────────────────────────────────────────

type fakePublisher struct {
	mu     sync.Mutex
	events []*domain.DomainEvent
	err    error
}

func (p *fakePublisher) Enqueue(_ context.Context, ev *domain.DomainEvent) error {
	if p.err != nil {
		return p.err
	}
	p.mu.Lock()
	p.events = append(p.events, ev)
	p.mu.Unlock()
	return nil
}

func (p *fakePublisher) eventTypes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.events))
	for i, e := range p.events {
		out[i] = e.Type
	}
	return out
}

// ── fakeTxRunner ───────────────────────────────────────────────────────────

type fakeTxRunner struct {
	pub *fakePublisher
	rec port.AuditRecorder // optional: injected into tx context for audit tests
	err error              // when set, RunInTx returns this without calling fn
}

func newTxRunner() *fakeTxRunner {
	return &fakeTxRunner{pub: &fakePublisher{}}
}

func (r *fakeTxRunner) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if r.err != nil {
		return r.err
	}
	txCtx := ctx
	if r.pub != nil {
		txCtx = port.WithEventPublisher(txCtx, r.pub)
	}
	if r.rec != nil {
		txCtx = port.WithAuditRecorder(txCtx, r.rec)
	}
	return fn(txCtx)
}

// ── fakeSnapshotRepo ───────────────────────────────────────────────────────

type fakeSnapshotRepo struct {
	mu        sync.Mutex
	snaps     map[uuid.UUID]*domain.EntitlementSnapshot
	getErr    error
	upsertErr error
	// if non-nil overrides the ENT-INV-1 logic and returns the given values
	upsertFn func(snap *domain.EntitlementSnapshot) (bool, error)
}

func newSnapshotRepo() *fakeSnapshotRepo {
	return &fakeSnapshotRepo{snaps: make(map[uuid.UUID]*domain.EntitlementSnapshot)}
}

func (r *fakeSnapshotRepo) Upsert(_ context.Context, snap *domain.EntitlementSnapshot) (bool, error) {
	if r.upsertFn != nil {
		return r.upsertFn(snap)
	}
	if r.upsertErr != nil {
		return false, r.upsertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.snaps[snap.TenantID]
	if ok && (existing.Version >= snap.Version || existing.SourceRecordVersion > snap.SourceRecordVersion) {
		return false, nil
	}
	cp := *snap
	r.snaps[snap.TenantID] = &cp
	return true, nil
}

func (r *fakeSnapshotRepo) GetLatest(_ context.Context, tenantID uuid.UUID) (*domain.EntitlementSnapshot, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snap, ok := r.snaps[tenantID]
	if !ok {
		return nil, domain.ErrSnapshotNotFound
	}
	cp := *snap
	return &cp, nil
}

// ── fakeOverrideRepo ───────────────────────────────────────────────────────

type fakeOverrideRepo struct {
	mu             sync.Mutex
	overrides      map[ovKey]*domain.Override
	history        []domain.OverrideHistory
	upsertErr      error
	deleteErr      error
	getErr         error
	listErr        error
	listExpiredErr error
	expiredList    []domain.Override
	listHistErr    error
}

func newOverrideRepo() *fakeOverrideRepo {
	return &fakeOverrideRepo{overrides: make(map[ovKey]*domain.Override)}
}

func (r *fakeOverrideRepo) Upsert(_ context.Context, o *domain.Override) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *o
	r.overrides[ovKey{o.TenantID, o.LimitKey}] = &cp
	return nil
}

func (r *fakeOverrideRepo) Delete(_ context.Context, tenantID uuid.UUID, key domain.LimitKey) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.overrides, ovKey{tenantID, key})
	return nil
}

func (r *fakeOverrideRepo) Get(_ context.Context, tenantID uuid.UUID, key domain.LimitKey) (*domain.Override, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ov, ok := r.overrides[ovKey{tenantID, key}]
	if !ok {
		return nil, domain.ErrOverrideNotFound
	}
	cp := *ov
	return &cp, nil
}

func (r *fakeOverrideRepo) ListByTenant(_ context.Context, tenantID uuid.UUID) ([]domain.Override, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Override
	for k, v := range r.overrides {
		if k.tenantID == tenantID {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *fakeOverrideRepo) ListExpired(_ context.Context, _ time.Time) ([]domain.Override, error) {
	if r.listExpiredErr != nil {
		return nil, r.listExpiredErr
	}
	return r.expiredList, nil
}

func (r *fakeOverrideRepo) InsertHistory(_ context.Context, h *domain.OverrideHistory) error {
	r.mu.Lock()
	r.history = append(r.history, *h)
	r.mu.Unlock()
	return nil
}

func (r *fakeOverrideRepo) ListHistory(_ context.Context, tenantID uuid.UUID, key domain.LimitKey) ([]domain.OverrideHistory, error) {
	if r.listHistErr != nil {
		return nil, r.listHistErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.OverrideHistory
	for _, h := range r.history {
		if h.TenantID == tenantID && h.LimitKey == key {
			out = append(out, h)
		}
	}
	return out, nil
}

// ── fakeAddonRepo ──────────────────────────────────────────────────────────

type fakeAddonRepo struct {
	mu             sync.Mutex
	addons         map[uuid.UUID]*domain.QuotaAddon
	insertErr      error
	updateErr      error
	getErr         error
	getBilErr      error
	listErr        error
	listActiveErr  error
	listExpiredErr error
	expiredList    []domain.QuotaAddon
	sumFn          func(tenantID uuid.UUID, key domain.LimitKey) (int64, error)
}

func newAddonRepo() *fakeAddonRepo {
	return &fakeAddonRepo{addons: make(map[uuid.UUID]*domain.QuotaAddon)}
}

func (r *fakeAddonRepo) Insert(_ context.Context, a *domain.QuotaAddon) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *a
	r.addons[a.ID] = &cp
	return nil
}

func (r *fakeAddonRepo) Update(_ context.Context, a *domain.QuotaAddon) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *a
	r.addons[a.ID] = &cp
	return nil
}

func (r *fakeAddonRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.QuotaAddon, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.addons[id]
	if !ok {
		return nil, domain.ErrAddonNotFound
	}
	cp := *a
	return &cp, nil
}

func (r *fakeAddonRepo) GetByBillingRef(_ context.Context, tenantID uuid.UUID, billingRef string) (*domain.QuotaAddon, error) {
	if r.getBilErr != nil {
		return nil, r.getBilErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.addons {
		if a.TenantID == tenantID && a.BillingRef != nil && *a.BillingRef == billingRef {
			cp := *a
			return &cp, nil
		}
	}
	return nil, domain.ErrAddonNotFound
}

func (r *fakeAddonRepo) ListByTenant(_ context.Context, tenantID uuid.UUID, limitKey *domain.LimitKey) ([]domain.QuotaAddon, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.QuotaAddon
	for _, a := range r.addons {
		if a.TenantID != tenantID {
			continue
		}
		if limitKey != nil && a.LimitKey != *limitKey {
			continue
		}
		out = append(out, *a)
	}
	return out, nil
}

func (r *fakeAddonRepo) ListExpired(_ context.Context, _ time.Time) ([]domain.QuotaAddon, error) {
	if r.listExpiredErr != nil {
		return nil, r.listExpiredErr
	}
	return r.expiredList, nil
}

func (r *fakeAddonRepo) ListActiveByTenantAndKey(_ context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddon, error) {
	if r.listActiveErr != nil {
		return nil, r.listActiveErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.QuotaAddon
	for _, a := range r.addons {
		if a.TenantID == tenantID && a.LimitKey == limitKey && a.Status == domain.AddonStatusActive && a.RemainingTokens > 0 {
			out = append(out, *a)
		}
	}
	return out, nil
}

func (r *fakeAddonRepo) SumRemainingByTenantAndKey(_ context.Context, tenantID uuid.UUID, key domain.LimitKey) (int64, error) {
	if r.sumFn != nil {
		return r.sumFn(tenantID, key)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var total int64
	for _, a := range r.addons {
		if a.TenantID == tenantID && a.LimitKey == key && a.Status == domain.AddonStatusActive {
			total += a.RemainingTokens
		}
	}
	return total, nil
}

// ── fakeRefundRepo ─────────────────────────────────────────────────────────

// fakeRefundRepo mirrors quota_addon_refunds: billing_ref is globally unique
// (Claim fails for any tenant), but GetForTenant only sees the caller's
// tenant, as RLS does.
type fakeRefundRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]domain.QuotaAddonRefund
}

func newRefundRepo() *fakeRefundRepo {
	return &fakeRefundRepo{rows: make(map[uuid.UUID]domain.QuotaAddonRefund)}
}

func (r *fakeRefundRepo) Claim(_ context.Context, ref *domain.QuotaAddonRefund) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rows[ref.BillingRef]; ok {
		return false, nil
	}
	r.rows[ref.BillingRef] = *ref
	return true, nil
}

func (r *fakeRefundRepo) SumByAddon(_ context.Context, tenantID uuid.UUID, addonIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := make(map[uuid.UUID]bool, len(addonIDs))
	for _, id := range addonIDs {
		want[id] = true
	}
	out := map[uuid.UUID]int64{}
	for _, row := range r.rows {
		if row.TenantID == tenantID && want[row.AddonID] {
			out[row.AddonID] += row.TokensRemoved
		}
	}
	return out, nil
}

func (r *fakeRefundRepo) GetForTenant(_ context.Context, tenantID, billingRef uuid.UUID) (*domain.QuotaAddonRefund, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[billingRef]
	if !ok || row.TenantID != tenantID {
		return nil, nil
	}
	cp := row
	return &cp, nil
}

// ── fakeDrawdownRepo ───────────────────────────────────────────────────────

type fakeDrawdownRepo struct {
	mu        sync.Mutex
	drawdowns map[uuid.UUID]*domain.QuotaAddonDrawdown
	byReqID   map[uuid.UUID]*domain.QuotaAddonDrawdown
	insertErr error
	getErr    error
}

func newDrawdownRepo() *fakeDrawdownRepo {
	return &fakeDrawdownRepo{
		drawdowns: make(map[uuid.UUID]*domain.QuotaAddonDrawdown),
		byReqID:   make(map[uuid.UUID]*domain.QuotaAddonDrawdown),
	}
}

func (r *fakeDrawdownRepo) Insert(_ context.Context, d *domain.QuotaAddonDrawdown) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *d
	r.drawdowns[d.ID] = &cp
	if d.RequestID != nil {
		r.byReqID[*d.RequestID] = &cp
	}
	return nil
}

func (r *fakeDrawdownRepo) GetByRequestID(_ context.Context, tenantID uuid.UUID, requestID uuid.UUID) (*domain.QuotaAddonDrawdown, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.byReqID[requestID]
	if !ok || d.TenantID != tenantID {
		return nil, nil
	}
	cp := *d
	return &cp, nil
}

func (r *fakeDrawdownRepo) ListByTenantAndKey(_ context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddonDrawdown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.QuotaAddonDrawdown
	for _, d := range r.drawdowns {
		if d.TenantID == tenantID && d.LimitKey == limitKey {
			out = append(out, *d)
		}
	}
	return out, nil
}

// ── fakeShortfallRepo ──────────────────────────────────────────────────────

type fakeShortfallRepo struct {
	mu         sync.Mutex
	shortfalls map[uuid.UUID]*domain.QuotaAddonShortfall
	insertErr  error
	updateErr  error
	getErr     error
	countFn    func(tenantID uuid.UUID, windowDays int) (int, error)
}

func newShortfallRepo() *fakeShortfallRepo {
	return &fakeShortfallRepo{shortfalls: make(map[uuid.UUID]*domain.QuotaAddonShortfall)}
}

func (r *fakeShortfallRepo) Insert(_ context.Context, s *domain.QuotaAddonShortfall) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.shortfalls[s.ID] = &cp
	return nil
}

func (r *fakeShortfallRepo) Update(_ context.Context, s *domain.QuotaAddonShortfall) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.shortfalls[s.ID] = &cp
	return nil
}

func (r *fakeShortfallRepo) GetByDrawdownID(_ context.Context, drawdownID uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.shortfalls {
		if s.DrawdownID == drawdownID {
			cp := *s
			return &cp, nil
		}
	}
	return nil, domain.ErrShortfallNotFound
}

func (r *fakeShortfallRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.shortfalls[id]
	if !ok {
		return nil, domain.ErrShortfallNotFound
	}
	cp := *s
	return &cp, nil
}

func (r *fakeShortfallRepo) ListByTenant(_ context.Context, tenantID uuid.UUID, statusFilter []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	filterSet := make(map[domain.ShortfallStatus]struct{}, len(statusFilter))
	for _, s := range statusFilter {
		filterSet[s] = struct{}{}
	}
	var out []domain.QuotaAddonShortfall
	for _, s := range r.shortfalls {
		if s.TenantID != tenantID {
			continue
		}
		if len(filterSet) > 0 {
			if _, ok := filterSet[s.Status]; !ok {
				continue
			}
		}
		out = append(out, *s)
	}
	return out, nil
}

func (r *fakeShortfallRepo) CountRecentForTenant(_ context.Context, tenantID uuid.UUID, windowDays int) (int, error) {
	if r.countFn != nil {
		return r.countFn(tenantID, windowDays)
	}
	return 0, nil
}

// ── fakeTrialRepo ──────────────────────────────────────────────────────────

type fakeTrialRepo struct {
	mu              sync.Mutex
	assignments     map[uuid.UUID]*domain.TenantTrialAssignment
	overrides       map[ovKey]*domain.TrialEntitlementOverride
	getAssignErr    error
	upsertAssignErr error
	upsertOvErr     error
	listOvErr       error
	deleteAllOvErr  error
	deleteAssignErr error
}

func newTrialRepo() *fakeTrialRepo {
	return &fakeTrialRepo{
		assignments: make(map[uuid.UUID]*domain.TenantTrialAssignment),
		overrides:   make(map[ovKey]*domain.TrialEntitlementOverride),
	}
}

func (r *fakeTrialRepo) UpsertAssignment(_ context.Context, a *domain.TenantTrialAssignment) error {
	if r.upsertAssignErr != nil {
		return r.upsertAssignErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *a
	r.assignments[a.TenantID] = &cp
	return nil
}

func (r *fakeTrialRepo) GetAssignment(_ context.Context, tenantID uuid.UUID) (*domain.TenantTrialAssignment, error) {
	if r.getAssignErr != nil {
		return nil, r.getAssignErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.assignments[tenantID]
	if !ok {
		return nil, domain.ErrTrialAssignmentNotFound
	}
	cp := *a
	return &cp, nil
}

func (r *fakeTrialRepo) DeleteAssignment(_ context.Context, tenantID uuid.UUID) error {
	if r.deleteAssignErr != nil {
		return r.deleteAssignErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.assignments, tenantID)
	return nil
}

func (r *fakeTrialRepo) UpsertOverride(_ context.Context, o *domain.TrialEntitlementOverride) error {
	if r.upsertOvErr != nil {
		return r.upsertOvErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *o
	r.overrides[ovKey{o.TenantID, o.LimitKey}] = &cp
	return nil
}

func (r *fakeTrialRepo) GetOverride(_ context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) (*domain.TrialEntitlementOverride, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.overrides[ovKey{tenantID, limitKey}]
	if !ok {
		return nil, domain.ErrTrialOverrideNotFound
	}
	cp := *o
	return &cp, nil
}

func (r *fakeTrialRepo) ListOverrides(_ context.Context, tenantID uuid.UUID) ([]domain.TrialEntitlementOverride, error) {
	if r.listOvErr != nil {
		return nil, r.listOvErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.TrialEntitlementOverride
	for k, v := range r.overrides {
		if k.tenantID == tenantID {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *fakeTrialRepo) DeleteOverride(_ context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.overrides, ovKey{tenantID, limitKey})
	return nil
}

func (r *fakeTrialRepo) DeleteAllOverrides(_ context.Context, tenantID uuid.UUID) error {
	if r.deleteAllOvErr != nil {
		return r.deleteAllOvErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.overrides {
		if k.tenantID == tenantID {
			delete(r.overrides, k)
		}
	}
	return nil
}

// ── fakeIAMClient ──────────────────────────────────────────────────────────

type fakeIAMClient struct {
	fetchPlanFn    func(ctx context.Context, planCode string) (*domain.Plan, error)
	fetchAllPlanFn func(ctx context.Context) ([]*domain.Plan, error)
	fetchSubFn     func(ctx context.Context, tenantID uuid.UUID) (*port.TenantSubscription, error)
}

func (f *fakeIAMClient) FetchPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	if f.fetchPlanFn != nil {
		return f.fetchPlanFn(ctx, planCode)
	}
	return samplePlan(planCode), nil
}

func (f *fakeIAMClient) FetchAllPlans(ctx context.Context) ([]*domain.Plan, error) {
	if f.fetchAllPlanFn != nil {
		return f.fetchAllPlanFn(ctx)
	}
	plans := []*domain.Plan{
		samplePlan("starter"),
		samplePlan("pro"),
		samplePlan("enterprise"),
	}
	return plans, nil
}

func (f *fakeIAMClient) FetchTenantSubscription(ctx context.Context, tenantID uuid.UUID) (*port.TenantSubscription, error) {
	if f.fetchSubFn != nil {
		return f.fetchSubFn(ctx, tenantID)
	}
	return sampleSub(tenantID, "starter", "active"), nil
}

// ── fakeCatalogSvc ─────────────────────────────────────────────────────────

type fakeCatalogSvc struct {
	getPlanFn     func(ctx context.Context, planCode string) (*domain.Plan, error)
	refreshPlanFn func(ctx context.Context, planCode string) (*domain.Plan, error)
	fanoutFn      func(ctx context.Context, planCode string) error
	listPlansFn   func(ctx context.Context) ([]string, error)
}

func (f *fakeCatalogSvc) GetPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	if f.getPlanFn != nil {
		return f.getPlanFn(ctx, planCode)
	}
	return samplePlan(planCode), nil
}

func (f *fakeCatalogSvc) RefreshPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	if f.refreshPlanFn != nil {
		return f.refreshPlanFn(ctx, planCode)
	}
	return samplePlan(planCode), nil
}

func (f *fakeCatalogSvc) FanoutCatalogRefresh(ctx context.Context, planCode string) error {
	if f.fanoutFn != nil {
		return f.fanoutFn(ctx, planCode)
	}
	return nil
}

func (f *fakeCatalogSvc) ListPlans(ctx context.Context) ([]string, error) {
	if f.listPlansFn != nil {
		return f.listPlansFn(ctx)
	}
	return nil, nil
}

// ── fakeCache ──────────────────────────────────────────────────────────────

type fakeCache struct {
	mu            sync.Mutex
	effectiveSets map[uuid.UUID]*domain.EffectiveSet
	plans         map[string]*domain.Plan
	ceilings      map[string]*int64

	getEffSetFn     func(ctx context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error)
	setEffSetErr    error
	setQuotaCeilErr error
	getPlanFn       func(ctx context.Context, planCode string) (*domain.Plan, error)
	setPlanErr      error
}

func newFakeCache() *fakeCache {
	return &fakeCache{
		effectiveSets: make(map[uuid.UUID]*domain.EffectiveSet),
		plans:         make(map[string]*domain.Plan),
		ceilings:      make(map[string]*int64),
	}
}

func (c *fakeCache) GetEffectiveSet(ctx context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error) {
	if c.getEffSetFn != nil {
		return c.getEffSetFn(ctx, tenantID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	es, ok := c.effectiveSets[tenantID]
	if !ok {
		return nil, nil
	}
	return es, nil
}

func (c *fakeCache) SetEffectiveSet(_ context.Context, tenantID uuid.UUID, es *domain.EffectiveSet) error {
	if c.setEffSetErr != nil {
		return c.setEffSetErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.effectiveSets[tenantID] = es
	return nil
}

func (c *fakeCache) InvalidateEffectiveSet(_ context.Context, tenantID uuid.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.effectiveSets, tenantID)
	return nil
}

func (c *fakeCache) SetQuotaCeiling(_ context.Context, tenantID uuid.UUID, limitKey domain.LimitKey, ceiling *int64) error {
	if c.setQuotaCeilErr != nil {
		return c.setQuotaCeilErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ceilings[tenantID.String()+":"+string(limitKey)] = ceiling
	return nil
}

func (c *fakeCache) ResetUsageCounter(_ context.Context, _ uuid.UUID, _ domain.LimitKey, _ string) error {
	return nil
}

func (c *fakeCache) DeleteUsageCounters(_ context.Context, _ domain.LimitKey, _ string) (int64, error) {
	return 0, nil
}

func (c *fakeCache) GetPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	if c.getPlanFn != nil {
		return c.getPlanFn(ctx, planCode)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.plans[planCode]
	if !ok {
		return nil, nil
	}
	return p, nil
}

func (c *fakeCache) SetPlan(_ context.Context, plan *domain.Plan) error {
	if c.setPlanErr != nil {
		return c.setPlanErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans[plan.Code] = plan
	return nil
}

func (c *fakeCache) InvalidatePlan(_ context.Context, planCode string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.plans, planCode)
	return nil
}

func (c *fakeCache) Health(_ context.Context) error { return nil }
func (c *fakeCache) Close() error                   { return nil }

// ── fakeCatalogCacheRepo ───────────────────────────────────────────────────

type fakeCatalogCacheRepo struct {
	plans     map[string]*domain.Plan
	planCodes []string
	getErr    error
}

func newCatalogCacheRepo() *fakeCatalogCacheRepo {
	return &fakeCatalogCacheRepo{plans: make(map[string]*domain.Plan)}
}

func (r *fakeCatalogCacheRepo) GetPlan(_ context.Context, planCode string) (*domain.Plan, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	p, ok := r.plans[planCode]
	if !ok {
		return nil, domain.ErrPlanNotFound
	}
	return p, nil
}

func (r *fakeCatalogCacheRepo) SetPlan(_ context.Context, plan *domain.Plan) error {
	r.plans[plan.Code] = plan
	return nil
}

func (r *fakeCatalogCacheRepo) InvalidatePlan(_ context.Context, planCode string) error {
	delete(r.plans, planCode)
	return nil
}

func (r *fakeCatalogCacheRepo) ListPlanCodes(_ context.Context) ([]string, error) {
	return r.planCodes, nil
}

// ── fakeTemporalStarter ────────────────────────────────────────────────────

type fakeTemporalStarter struct {
	calls            []string
	catalogFanoutErr error
}

func (f *fakeTemporalStarter) StartCatalogFanout(_ context.Context, planCode string) error {
	f.calls = append(f.calls, planCode)
	return f.catalogFanoutErr
}

func (f *fakeTemporalStarter) StartExpiryFanout(_ context.Context) error {
	return nil
}

// ── errSentinel ────────────────────────────────────────────────────────────

var errStorage = errors.New("storage failure")

// ── nopAuditRecorder ────────────────────────────────────────────────────────

// nopAuditRecorder satisfies port.AuditRecorder but discards every entry.
// Used when tests want an audit recorder in context without real persistence.
type nopAuditRecorder struct{}

func (nopAuditRecorder) Record(_ context.Context, _ port.AuditEntry) error        { return nil }
func (nopAuditRecorder) RecordBatch(_ context.Context, _ []port.AuditEntry) error { return nil }

// newTxRunnerWithAuditRecorder returns a fakeTxRunner that injects the given
// audit recorder into every transaction context it creates.
func newTxRunnerWithAuditRecorder(rec port.AuditRecorder) *fakeTxRunner {
	tx := newTxRunner()
	tx.rec = rec
	return tx
}
