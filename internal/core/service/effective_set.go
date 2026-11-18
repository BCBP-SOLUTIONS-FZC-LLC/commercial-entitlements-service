// Package service implements the core business logic for the Commercial
// Entitlements service. It has no dependency on frameworks, infrastructure or
// adapters — only port interfaces and domain types.
package service

import (
	"context"
	"errors"
	"fmt"
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
	snapshots  port.SnapshotRepository
	overrides  port.OverrideRepository
	addons     port.AddonRepository
	trials     port.TrialRepository
	catalog    port.CatalogService
	iamClient  port.IAMClient
	cache      port.Cache
	txRunner   port.TxRunner
	log        port.Logger
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

	// --- Step 2: Trial-profile overrides ---
	var trialProfile *string
	var trialAssignment *domain.TenantTrialAssignment
	if state == domain.EntitlementStateTrial {
		if ta, err := s.trials.GetAssignment(ctx, tenantID); err == nil {
			trialAssignment = ta
			p := string(ta.Profile)
			trialProfile = &p
			// Apply trial-profile limit overrides.
			if profileLimits, ok := plan.TrialProfiles[string(ta.Profile)]; ok {
				applyTrialProfileLimits(limits, profileLimits)
			}
		}
	}
	_ = trialAssignment // used for profile context

	// --- Step 3: Trial entitlement overrides ---
	if state == domain.EntitlementStateTrial {
		trialOvs, err := s.trials.ListOverrides(ctx, tenantID)
		if err == nil {
			for i := range trialOvs {
				limits[trialOvs[i].LimitKey] = domain.LimitValue{Value: trialOvs[i].Value}
			}
		}
	}

	// --- Step 4: Operator overrides ---
	opOvs, err := s.overrides.ListByTenant(ctx, tenantID)
	if err == nil {
		now := time.Now().UTC()
		for i := range opOvs {
			ov := &opOvs[i]
			if ov.ExpiresAt != nil && ov.ExpiresAt.Before(now) {
				continue // skip expired; reconciler sweeps these
			}
			limits[ov.LimitKey] = domain.LimitValue{Value: ov.Value}
		}
	}

	// --- Step 5: Addon quota balance (additive) ---
	for _, key := range []domain.LimitKey{
		domain.LimitKeyLLMTokenQuotaMonthly,
		domain.LimitKeyAPIRequestBudgetMonthly,
	} {
		addonBalance, err := s.addons.SumRemainingByTenantAndKey(ctx, tenantID, key)
		if err == nil && addonBalance > 0 {
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
	computedAt := time.Now().UTC()
	es := &domain.EffectiveSet{
		TenantID:     tenantID,
		PlanCode:     sub.PlanCode,
		State:        state,
		FeatureFlags: plan.FeatureFlags(),
		ModelTier:    plan.ModelTier,
		Limits:       limits,
		TrialProfile: trialProfile,
		Cause:        cause,
		ComputedAt:   computedAt,
	}

	// ENT-INV-6 no-op guard: compare against latest snapshot.
	existingSnap, snapErr := s.snapshots.GetLatest(ctx, tenantID)
	var nextVersion int64 = 1
	if snapErr == nil {
		// Check if computed set is effectively identical.
		if effectiveSetUnchanged(es, existingSnap) {
			// Return existing EffectiveSet from snapshot without writing.
			s.logger().Debug("recompute: effective set unchanged, skipping write",
				map[string]interface{}{"tenant_id": tenantID, "version": existingSnap.Version})
			existing := snapToEffectiveSet(existingSnap)
			return existing, nil
		}
		nextVersion = existingSnap.Version + 1
	}
	es.Version = nextVersion

	snap := effectiveSetToSnap(es)

	// Persist snapshot + emit event within single transaction.
	var finalES *domain.EffectiveSet
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.snapshots.Upsert(txCtx, snap); err != nil {
			return fmt.Errorf("upsert snapshot: %w", err)
		}

		// Emit TenantEntitlementsChanged event.
		pub, ok := port.EventPublisherFromContext(txCtx)
		if ok {
			ev := domain.NewTenantEvent(domain.EventTypeTenantEntitlementsChanged, tenantID,
				domain.TenantEntitlementsChangedPayload{
					TenantID:        tenantID,
					Version:         es.Version,
					Cause:           cause,
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

	// ENT-D13: write quota ceiling to Valkey (advisory, best-effort).
	if s.cache != nil {
		s.writeQuotaCeiling(ctx, tenantID, finalES.Limits)
		// Invalidate effective set cache; re-warm with new value.
		_ = s.cache.SetEffectiveSet(ctx, tenantID, finalES)
	}

	return finalES, nil
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

func snapToEffectiveSet(snap *domain.EntitlementSnapshot) *domain.EffectiveSet {
	return &domain.EffectiveSet{
		TenantID:     snap.TenantID,
		Version:      snap.Version,
		PlanCode:     snap.Plan,
		State:        snap.State,
		FeatureFlags: snap.FeatureFlags,
		ModelTier:    snap.ModelTier,
		Limits:       snap.Limits,
		TrialProfile: snap.TrialProfile,
		Cause:        snap.Cause,
		ComputedAt:   snap.ComputedAt,
	}
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
// domain.EntitlementState. Unknown states default to EntitlementStateSuspended.
func mapSubscriptionState(state string) domain.EntitlementState {
	switch state {
	case "active":
		return domain.EntitlementStateActive
	case "trial":
		return domain.EntitlementStateTrial
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
