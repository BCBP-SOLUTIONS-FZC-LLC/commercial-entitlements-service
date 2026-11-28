// Package service implements the core business logic for the Commercial
// Entitlements service. It has no dependency on frameworks, infrastructure or
// adapters — only port interfaces and domain types.
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// EffectiveSetService computes a tenant's merged entitlement set from:
//  1. Plan baseline (from CatalogService)
//  2. Trial-profile limit overrides (when tenant is in trial)
//  3. Trial-scoped entitlement overrides set by operators
//  4. Operator manual overrides
//  5. Quota addon balance (additive on top of plan baseline)
//
// ENT-INV-6: Recompute is idempotent — if the computed set is byte-identical
// to the latest snapshot the write and event emission are skipped.
type EffectiveSetService struct {
	snapshots port.SnapshotRepository
	overrides port.OverrideRepository
	addons    port.AddonRepository
	trials    port.TrialRepository
	catalog   port.CatalogService
	iamClient port.IAMClient
	cache     port.Cache
	txRunner  port.TxRunner
	log       port.Logger

	// onRecompute is an optional observability hook injected by the wiring
	// layer. It fires after every Recompute attempt. Keeping it here (not in
	// a port interface) lets the core layer stay free of adapter imports while
	// the metrics package can still register a closure against it.
	onRecompute func(cause domain.EffectiveSetCause, noop, failed bool, d time.Duration)
}

// Ensure EffectiveSetService implements port.EffectiveSetService at compile time.
var _ port.EffectiveSetService = (*EffectiveSetService)(nil)

// NewEffectiveSetService wires up the EffectiveSetService.
func NewEffectiveSetService(
	snapshots port.SnapshotRepository,
	overrides port.OverrideRepository,
	addons port.AddonRepository,
	trials port.TrialRepository,
	catalog port.CatalogService,
	iamClient port.IAMClient,
	cache port.Cache,
	txRunner port.TxRunner,
) *EffectiveSetService {
	return &EffectiveSetService{
		snapshots: snapshots,
		overrides: overrides,
		addons:    addons,
		trials:    trials,
		catalog:   catalog,
		iamClient: iamClient,
		cache:     cache,
		txRunner:  txRunner,
	}
}

// WithLogger attaches a logger.
func (s *EffectiveSetService) WithLogger(l port.Logger) *EffectiveSetService {
	s.log = l
	return s
}

// WithRecomputeHook registers an observability callback invoked after every
// Recompute attempt. The wiring layer (cmd/server/main.go) injects the
// metrics implementation; tests may inject a recording stub.
func (s *EffectiveSetService) WithRecomputeHook(fn func(cause domain.EffectiveSetCause, noop, failed bool, d time.Duration)) *EffectiveSetService {
	s.onRecompute = fn
	return s
}

func (s *EffectiveSetService) logger() port.Logger {
	if s.log != nil {
		return s.log
	}
	return port.NopLogger{}
}

// GetEffectiveSet returns the latest cached/snapshotted entitlement set for the
// tenant. It does NOT recompute; use Recompute for a fresh computation.
func (s *EffectiveSetService) GetEffectiveSet(ctx context.Context, tenantID uuid.UUID) (*domain.EffectiveSet, error) {
	// Advisory cache first.
	if s.cache != nil {
		if es, err := s.cache.GetEffectiveSet(ctx, tenantID); err == nil && es != nil {
			return es, nil
		}
	}
	// Fall through to Postgres snapshot.
	snap, err := s.snapshots.GetLatest(ctx, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrSnapshotNotFound) {
			return nil, domain.NewError(domain.ErrSnapshotNotFound, "no entitlement snapshot for tenant")
		}
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	es := snapToEffectiveSet(snap)
	// Warm cache (best-effort).
	if s.cache != nil {
		_ = s.cache.SetEffectiveSet(ctx, tenantID, es)
	}
	return es, nil
}

// Recompute triggers a full recomputation of the tenant's effective entitlement
// set. It applies the merge pipeline in order:
//  1. Plan baseline
//  2. Trial-profile limits (when trial assignment present)
//  3. Trial entitlement overrides (operator-set during trial)
//  4. Operator override per-key
//  5. Quota addon totals (additive)
//
// ENT-INV-6: if the computed set is identical to the latest snapshot (same
// version hash), the write and event emission are skipped (no-op guard).
//
// ENT-D13: after every recomputation, write the quota ceiling for
// llm_token_quota_monthly to Valkey so the downstream quota enforcer can
// rate-gate without hitting Postgres on every request.
func (s *EffectiveSetService) Recompute(ctx context.Context, tenantID uuid.UUID, cause domain.EffectiveSetCause) (*domain.EffectiveSet, error) {
	if s.onRecompute == nil {
		return s.recomputeCore(ctx, tenantID, cause, nil)
	}
	start := time.Now()
	noop := false
	es, err := s.recomputeCore(ctx, tenantID, cause, &noop)
	s.onRecompute(cause, noop, err != nil, time.Since(start))
	return es, err
}

// recomputeCore contains the full recompute logic. noopOut, when non-nil, is
// set to true when ENT-INV-6 fires (no write, no event emitted).
func (s *EffectiveSetService) recomputeCore(ctx context.Context, tenantID uuid.UUID, cause domain.EffectiveSetCause, noopOut *bool) (*domain.EffectiveSet, error) {
	// Fetch the upstream subscription to get plan code and state.
	sub, err := s.iamClient.FetchTenantSubscription(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("fetch subscription: %w", err)
	}

	// Fetch plan definition.
	plan, err := s.catalog.GetPlan(ctx, sub.PlanCode)
	if err != nil {
		return nil, fmt.Errorf("get plan %q: %w", sub.PlanCode, err)
	}

	// Build limits map starting from plan baseline.
	limits := plan.BaseLimits()

	// Map subscription state → EntitlementState.
	state := mapSubscriptionState(sub.State)

	// --- Step 2: Trial-profile limits ---
	// No tenant_trial_assignments row means the default Trial-Specific
	// profile (CD-6, LLD §4.2), never the paid plan's limits (E-6).
	var trialProfile *string
	if state == domain.EntitlementStateTrial {
		profile := domain.TrialProfileTrialSpecific
		ta, err := s.trials.GetAssignment(ctx, tenantID)
		switch {
		case err == nil && ta != nil:
			profile = ta.Profile
		case err == nil, errors.Is(err, domain.ErrTrialAssignmentNotFound):
			// Default profile.
		default:
			// Unexpected repository error — fail the recompute.
			return nil, fmt.Errorf("get trial assignment: %w", err)
		}
		p := string(profile)
		trialProfile = &p
		if base, ok := trialProfileBase(profile); ok {
			if profileLimits, found := plan.TrialProfiles[string(base)]; found {
				applyTrialProfileLimits(limits, profileLimits)
			} else {
				// Catalog published no entry: the trial falls back to the paid
				// plan's limits (unlimited on Enterprise). Make it visible.
				s.logger().Warn("recompute: catalog has no trial profile entry; using plan limits",
					map[string]interface{}{"tenant_id": tenantID, "plan_code": sub.PlanCode, "profile": string(base)})
			}
		}
	}

	// --- Step 3: Trial entitlement overrides ---
	if state == domain.EntitlementStateTrial {
		trialOvs, err := s.trials.ListOverrides(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("list trial overrides: %w", err)
		}
		for i := range trialOvs {
			limits[trialOvs[i].LimitKey] = domain.LimitValue{Value: trialOvs[i].Value}
		}
	}

	// --- Step 4: Operator overrides ---
	opOvs, err := s.overrides.ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list operator overrides: %w", err)
	}
	now := time.Now().UTC()
	for i := range opOvs {
		ov := &opOvs[i]
		if ov.ExpiresAt != nil && ov.ExpiresAt.Before(now) {
			continue // skip expired; reconciler sweeps these
		}
		limits[ov.LimitKey] = domain.LimitValue{Value: ov.Value}
	}

	// --- Step 5: Addon quota balance (additive) ---
	for _, key := range []domain.LimitKey{
		domain.LimitKeyLLMTokenQuotaMonthly,
		domain.LimitKeyAPIRequestBudgetMonthly,
	} {
		addonBalance, err := s.addons.SumRemainingByTenantAndKey(ctx, tenantID, key)
		if err != nil {
			return nil, fmt.Errorf("sum addon balance for %s: %w", key, err)
		}
		if addonBalance > 0 {
			current := limits[key]
			if current.IsUnlimited() {
				continue // plan already unlimited; addons don't change that
			}
			if current.Value != nil {
				newVal := *current.Value + addonBalance
				limits[key] = domain.LimitValue{Value: &newVal}
			}
		}
	}

	// Build computed EffectiveSet.
	// Merge plan-baseline feature flags with per-tenant operator overrides from
	// iam-org-membership (O-4 PATCH /operator/tenants/:id/feature-flags, returned
	// on I-17 plan-context as feature_flags[]). Plan flags are the foundation;
	// tenant-delta flags are unioned on top (GAP-B fix).
	computedAt := time.Now().UTC()
	es := &domain.EffectiveSet{
		TenantID:         tenantID,
		PlanCode:         sub.PlanCode,
		State:            state,
		FeatureFlags:     mergeFeatureFlags(plan.FeatureFlags(), sub.FeatureFlags),
		ModelTier:        plan.ModelTier,
		Limits:           limits,
		TrialProfile:     trialProfile,
		Cause:            cause,
		ComputedAt:       computedAt,
		AddonEligibility: computeAddonEligibility(state, limits),
	}

	// ENT-INV-6 no-op guard: compare against latest snapshot.
	existingSnap, snapErr := s.snapshots.GetLatest(ctx, tenantID)
	var nextVersion int64 = 1
	if snapErr == nil {
		// Check if computed set is effectively identical.
		if effectiveSetUnchanged(es, existingSnap) {
			// ENT-INV-6: return existing EffectiveSet without writing.
			s.logger().Debug("recompute: effective set unchanged, skipping write",
				map[string]interface{}{"tenant_id": tenantID, "version": existingSnap.Version})
			if noopOut != nil {
				*noopOut = true
			}
			existing := snapToEffectiveSet(existingSnap)
			return existing, nil
		}
		nextVersion = existingSnap.Version + 1
	}
	es.Version = nextVersion

	snap := effectiveSetToSnap(es)
	// Stamp the Core tenant row version this set was computed from, so the
	// upsert refuses to overwrite a snapshot built from a newer I-17 read.
	// Without it, a recompute that read the old plan but reached the upsert
	// last (e.g. Billing's event racing Core's TenantStateChanged relay, B-3)
	// would win on version and keep the old plan.
	snap.SourceRecordVersion = sub.RecordVersion

	// Pre-compute which limit keys changed so the event payload is populated.
	changedKeys := computeChangedKeys(es.Limits, existingSnap)

	// Persist snapshot + emit event within single transaction.
	// written=false means a concurrent recompute already committed the same or
	// a newer version; we skip event emission to prevent duplicate events.
	var (
		finalES *domain.EffectiveSet
		written bool
	)
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		var upsertErr error
		written, upsertErr = s.snapshots.Upsert(txCtx, snap)
		if upsertErr != nil {
			return fmt.Errorf("upsert snapshot: %w", upsertErr)
		}
		if !written {
			// Concurrent recompute committed a same/newer version first.
			return nil
		}

		// Emit TenantEntitlementsChanged event.
		pub, ok := port.EventPublisherFromContext(txCtx)
		if ok {
			ev := domain.NewTenantEvent(domain.EventTypeTenantEntitlementsChanged, tenantID,
				domain.TenantEntitlementsChangedPayload{
					TenantID:        tenantID,
					Version:         es.Version,
					Cause:           cause,
					ChangedKeys:     changedKeys,
					PreviousVersion: nextVersion - 1,
					ComputedAt:      computedAt,
				},
			)
			if err := pub.Enqueue(txCtx, ev); err != nil {
				return fmt.Errorf("enqueue event: %w", err)
			}
		}
		finalES = es
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	if !written {
		// Another concurrent recompute won the race; treat as noop.
		if noopOut != nil {
			*noopOut = true
		}
		return es, nil
	}

	// ENT-D13: write quota ceiling to Valkey (advisory, best-effort).
	if s.cache != nil {
		s.writeQuotaCeiling(ctx, tenantID, finalES.Limits)
		// Invalidate effective set cache; re-warm with new value.
		_ = s.cache.SetEffectiveSet(ctx, tenantID, finalES)
	}

	return finalES, nil
}

// computeChangedKeys returns the limit keys that differ between the new limits
// map and the previous snapshot. All keys are returned as changed when there is
// no previous snapshot (first recompute for the tenant).
func computeChangedKeys(newLimits map[domain.LimitKey]domain.LimitValue, prev *domain.EntitlementSnapshot) []string {
	if prev == nil {
		keys := make([]string, 0, len(newLimits))
		for k := range newLimits {
			keys = append(keys, string(k))
		}
		return keys
	}
	var changed []string
	seen := make(map[domain.LimitKey]struct{}, len(newLimits))
	for k, v := range newLimits {
		seen[k] = struct{}{}
		if old, ok := prev.Limits[k]; !ok || !limitValEqual(v, old) {
			changed = append(changed, string(k))
		}
	}
	for k := range prev.Limits {
		if _, ok := seen[k]; !ok {
			changed = append(changed, string(k))
		}
	}
	return changed
}

func limitValEqual(a, b domain.LimitValue) bool {
	if a.IsUnlimited() != b.IsUnlimited() {
		return false
	}
	if a.IsUnlimited() {
		return true
	}
	return *a.Value == *b.Value
}

// writeQuotaCeiling writes ENT-D13 quota ceiling key to Valkey.
func (s *EffectiveSetService) writeQuotaCeiling(ctx context.Context, tenantID uuid.UUID, limits map[domain.LimitKey]domain.LimitValue) {
	for key, lv := range limits {
		if key == domain.LimitKeyLLMTokenQuotaMonthly {
			if err := s.cache.SetQuotaCeiling(ctx, tenantID, key, lv.Value); err != nil {
				s.logger().Warn("ENT-D13: failed to write quota ceiling to Valkey",
					map[string]interface{}{"tenant_id": tenantID, "key": key, "error": err.Error()})
			}
		}
	}
}

// effectiveSetUnchanged compares two sets to detect no-op recomputations.
func effectiveSetUnchanged(es *domain.EffectiveSet, snap *domain.EntitlementSnapshot) bool {
	if es.PlanCode != snap.Plan || es.State != snap.State || es.ModelTier != snap.ModelTier {
		return false
	}
	if !stringSlicesEqual(es.FeatureFlags, snap.FeatureFlags) {
		return false
	}
	// trial_profile is part of the ENT-I1 contract: a profile change with
	// identical limits (e.g. trial_specific → custom, or the E-6 NULL →
	// trial_specific backfill) must still write the snapshot.
	if !optionalStringEqual(es.TrialProfile, snap.TrialProfile) {
		return false
	}
	if len(es.Limits) != len(snap.Limits) {
		return false
	}
	for k, v := range es.Limits {
		sv, ok := snap.Limits[k]
		if !ok {
			return false
		}
		if v.IsUnlimited() != sv.IsUnlimited() {
			return false
		}
		if !v.IsUnlimited() && *v.Value != *sv.Value {
			return false
		}
	}
	return true
}

// optionalStringEqual reports whether two optional strings are both nil or hold the same value.
func optionalStringEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// stringSlicesEqual reports whether two sorted string slices are identical.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mergeFeatureFlags unions planFlags (plan baseline) with tenantFlags
// (per-tenant operator delta from iam-org-membership I-17 feature_flags[]).
// The result is deduped and sorted for stable comparison in effectiveSetUnchanged.
func mergeFeatureFlags(planFlags, tenantFlags []string) []string {
	if len(tenantFlags) == 0 {
		out := make([]string, len(planFlags))
		copy(out, planFlags)
		sort.Strings(out)
		return out
	}
	seen := make(map[string]struct{}, len(planFlags)+len(tenantFlags))
	merged := make([]string, 0, len(planFlags)+len(tenantFlags))
	for _, f := range planFlags {
		if _, ok := seen[f]; !ok {
			seen[f] = struct{}{}
			merged = append(merged, f)
		}
	}
	for _, f := range tenantFlags {
		if _, ok := seen[f]; !ok {
			seen[f] = struct{}{}
			merged = append(merged, f)
		}
	}
	sort.Strings(merged)
	return merged
}

func snapToEffectiveSet(snap *domain.EntitlementSnapshot) *domain.EffectiveSet {
	return &domain.EffectiveSet{
		TenantID:         snap.TenantID,
		Version:          snap.Version,
		PlanCode:         snap.Plan,
		State:            snap.State,
		FeatureFlags:     snap.FeatureFlags,
		ModelTier:        snap.ModelTier,
		Limits:           snap.Limits,
		TrialProfile:     snap.TrialProfile,
		Cause:            snap.Cause,
		ComputedAt:       snap.ComputedAt,
		AddonEligibility: computeAddonEligibility(snap.State, snap.Limits),
	}
}

// computeAddonEligibility returns per-resource eligibility for quota add-on purchases (BL-OQ-3).
// Keys: "llm_tokens" → LLMTokenQuotaMonthly, "api_requests" → APIRequestBudgetMonthly.
// Reasons: "not_active" (state neither active nor trial), "trial" (api_requests
// during a trial: only token add-ons may be bought in a trial), "unlimited_quota"
// (resource is unlimited). A trial tenant may buy llm_tokens packs (HLD v0.69
// P-5, E-10) unless its token entitlement is unlimited (P-5d); an expired trial
// is suspended and gets not_active (P-5f). Billing enforces the trial cap,
// owner-only and USD rules (P-5b, P-5c, P-5e).
func computeAddonEligibility(state domain.EntitlementState, limits map[domain.LimitKey]domain.LimitValue) map[string]domain.AddonResourceEligibility {
	type resourceDef struct {
		key      domain.LimitKey
		shortKey string
	}
	resources := []resourceDef{
		{domain.LimitKeyLLMTokenQuotaMonthly, "llm_tokens"},
		{domain.LimitKeyAPIRequestBudgetMonthly, "api_requests"},
	}

	result := make(map[string]domain.AddonResourceEligibility, len(resources))

	var stateReason *string
	if state == domain.EntitlementStateTrial {
		r := "trial"
		stateReason = &r
	} else if state != domain.EntitlementStateActive {
		r := "not_active"
		stateReason = &r
	}

	for _, r := range resources {
		// Token add-ons are allowed during a trial (P-5): skip the state gate.
		trialTokens := state == domain.EntitlementStateTrial && r.key == domain.LimitKeyLLMTokenQuotaMonthly
		if stateReason != nil && !trialTokens {
			result[r.shortKey] = domain.AddonResourceEligibility{Eligible: false, Reason: stateReason}
			continue
		}
		if lv, ok := limits[r.key]; !ok || lv.IsUnlimited() {
			reason := "unlimited_quota"
			result[r.shortKey] = domain.AddonResourceEligibility{Eligible: false, Reason: &reason}
		} else {
			result[r.shortKey] = domain.AddonResourceEligibility{Eligible: true, Reason: nil}
		}
	}
	return result
}

func effectiveSetToSnap(es *domain.EffectiveSet) *domain.EntitlementSnapshot {
	return &domain.EntitlementSnapshot{
		TenantID:     es.TenantID,
		Version:      es.Version,
		Plan:         es.PlanCode,
		State:        es.State,
		FeatureFlags: es.FeatureFlags,
		ModelTier:    es.ModelTier,
		Limits:       es.Limits,
		TrialProfile: es.TrialProfile,
		Cause:        es.Cause,
		ComputedAt:   es.ComputedAt,
	}
}

// mapSubscriptionState converts the upstream subscription state string to a
// domain.EntitlementState. Covers both iam-org-membership's SubscriptionStatus
// values and any internal grace_period/provisioning values.
// Unknown states default to EntitlementStateSuspended (fail-safe restriction).
func mapSubscriptionState(state string) domain.EntitlementState {
	switch state {
	case "active":
		return domain.EntitlementStateActive
	case "trial":
		return domain.EntitlementStateTrial
	// iam-org-membership billing-delinquency states → grace window
	case "past_due", "cancelled":
		return domain.EntitlementStateGracePeriod
	// iam-org-membership trial that has elapsed → suspended (awaiting conversion)
	case "trial_expired":
		return domain.EntitlementStateSuspended
	case "grace_period":
		return domain.EntitlementStateGracePeriod
	case "suspended":
		return domain.EntitlementStateSuspended
	case "offboarded":
		return domain.EntitlementStateOffboarded
	case "provisioning":
		return domain.EntitlementStateProvisioning
	default:
		return domain.EntitlementStateSuspended
	}
}

// trialProfileBase returns the Catalog trial_profiles entry a profile draws its
// limits from (CD-51). full_plan uses the plan row directly (ok=false); custom
// uses Trial-Specific as its base, with trial overrides layered on in step 3.
func trialProfileBase(p domain.TrialProfile) (domain.TrialProfile, bool) {
	switch p {
	case domain.TrialProfileTrialSpecific, domain.TrialProfileCustom:
		return domain.TrialProfileTrialSpecific, true
	case domain.TrialProfileReducedPlan:
		return domain.TrialProfileReducedPlan, true
	case domain.TrialProfileFullPlan:
		return "", false
	default:
		return "", false
	}
}

// applyTrialProfileLimits overwrites limits with the trial-profile values where
// specified.
func applyTrialProfileLimits(limits map[domain.LimitKey]domain.LimitValue, p domain.TrialProfileLimits) {
	if p.LLMTokenQuotaMonthly != nil {
		limits[domain.LimitKeyLLMTokenQuotaMonthly] = domain.LimitValue{Value: p.LLMTokenQuotaMonthly}
	}
	if p.APIRequestBudgetMonthly != nil {
		limits[domain.LimitKeyAPIRequestBudgetMonthly] = domain.LimitValue{Value: p.APIRequestBudgetMonthly}
	}
	if p.APIRateLimitRPS != nil {
		v := int64(*p.APIRateLimitRPS)
		limits[domain.LimitKeyAPIRateLimitRPS] = domain.LimitValue{Value: &v}
	}
	if p.WorkflowTemplateLimit != nil {
		v := int64(*p.WorkflowTemplateLimit)
		limits[domain.LimitKeyWorkflowTemplateLimit] = domain.LimitValue{Value: &v}
	}
	if p.TenderLimit != nil {
		v := int64(*p.TenderLimit)
		limits[domain.LimitKeyTenderLimit] = domain.LimitValue{Value: &v}
	}
}
