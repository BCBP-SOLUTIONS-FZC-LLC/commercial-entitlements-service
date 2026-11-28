package unit_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

// E-6: trial profile resolution in computeEffectiveSet (CD-6, CD-51, LLD §5.4
// null-semantics table). Catalog publishes trial_profiles keyed by
// trial_specific and reduced_plan only.

const (
	tsLLM int64 = 150_000
	tsAPI int64 = 50_000
	rpLLM int64 = 250_000
)

func trialProfilePlan() *domain.Plan {
	plan := samplePlan("enterprise")
	plan.LLMTokenQuotaMonthly = nil // unlimited on the paid plan
	plan.APIRequestBudgetMonthly = nil
	tsRPS, tsWF, tsTender := 5, 3, 5
	llm, api := tsLLM, tsAPI
	rp := rpLLM
	plan.TrialProfiles = map[string]domain.TrialProfileLimits{
		string(domain.TrialProfileTrialSpecific): {
			LLMTokenQuotaMonthly:    &llm,
			APIRequestBudgetMonthly: &api,
			APIRateLimitRPS:         &tsRPS,
			WorkflowTemplateLimit:   &tsWF,
			TenderLimit:             &tsTender,
		},
		string(domain.TrialProfileReducedPlan): {LLMTokenQuotaMonthly: &rp},
	}
	return plan
}

func recomputeTrialTenant(t *testing.T, status string, profile *domain.TrialProfile, trialOvs map[domain.LimitKey]int64) *domain.EffectiveSet {
	t.Helper()
	tenantID := uuid.New()
	plan := trialProfilePlan()
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, id uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(id, "enterprise", status), nil
		},
	}
	catalog := &fakeCatalogSvc{getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil }}
	trials := newTrialRepo()
	now := time.Now().UTC()
	if profile != nil {
		require.NoError(t, trials.UpsertAssignment(context.Background(), &domain.TenantTrialAssignment{
			TenantID: tenantID, PlanCode: "enterprise", Profile: *profile,
			StartsAt: now, EndsAt: now.Add(30 * 24 * time.Hour), RecordVersion: 1,
		}))
	}
	for k, v := range trialOvs {
		val := v
		require.NoError(t, trials.UpsertOverride(context.Background(), &domain.TrialEntitlementOverride{
			TenantID: tenantID, LimitKey: k, Value: &val, CreatedAt: now, UpdatedAt: now,
		}))
	}
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials, catalog, iam, nil, newTxRunner())
	es, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	return es
}

func limitValue(t *testing.T, es *domain.EffectiveSet, k domain.LimitKey) *int64 {
	t.Helper()
	lv, ok := es.Limits[k]
	require.True(t, ok, "limit %s missing", k)
	return lv.Value
}

func profilePtr(p domain.TrialProfile) *domain.TrialProfile { return &p }

func TestTrialProfile_NoAssignment_DefaultsToTrialSpecific(t *testing.T) {
	es := recomputeTrialTenant(t, "trial", nil, nil)
	require.NotNil(t, es.TrialProfile)
	assert.Equal(t, "trial_specific", *es.TrialProfile)
	require.NotNil(t, limitValue(t, es, domain.LimitKeyLLMTokenQuotaMonthly), "default trial must not inherit the unlimited paid quota")
	assert.Equal(t, tsLLM, *limitValue(t, es, domain.LimitKeyLLMTokenQuotaMonthly))
	assert.Equal(t, tsAPI, *limitValue(t, es, domain.LimitKeyAPIRequestBudgetMonthly))
	assert.Equal(t, int64(5), *limitValue(t, es, domain.LimitKeyAPIRateLimitRPS))
	assert.Equal(t, int64(3), *limitValue(t, es, domain.LimitKeyWorkflowTemplateLimit))
	assert.Equal(t, int64(5), *limitValue(t, es, domain.LimitKeyTenderLimit))
}

func TestTrialProfile_ExplicitTrialSpecific(t *testing.T) {
	es := recomputeTrialTenant(t, "trial", profilePtr(domain.TrialProfileTrialSpecific), nil)
	assert.Equal(t, "trial_specific", *es.TrialProfile)
	assert.Equal(t, tsLLM, *limitValue(t, es, domain.LimitKeyLLMTokenQuotaMonthly))
}

func TestTrialProfile_ReducedPlan_NullColumnsInheritPlan(t *testing.T) {
	es := recomputeTrialTenant(t, "trial", profilePtr(domain.TrialProfileReducedPlan), nil)
	assert.Equal(t, "reduced_plan", *es.TrialProfile)
	assert.Equal(t, rpLLM, *limitValue(t, es, domain.LimitKeyLLMTokenQuotaMonthly))
	// reduced_plan has no api budget value → inherit the plan's (unlimited) value (PCC-4).
	assert.Nil(t, limitValue(t, es, domain.LimitKeyAPIRequestBudgetMonthly))
}

func TestTrialProfile_FullPlan_UsesPlanLimits(t *testing.T) {
	es := recomputeTrialTenant(t, "trial", profilePtr(domain.TrialProfileFullPlan), nil)
	assert.Equal(t, "full_plan", *es.TrialProfile)
	assert.Nil(t, limitValue(t, es, domain.LimitKeyLLMTokenQuotaMonthly))
	assert.Equal(t, int64(100), *limitValue(t, es, domain.LimitKeyAPIRateLimitRPS))
}

func TestTrialProfile_Custom_TrialSpecificBasePlusOverrides(t *testing.T) {
	es := recomputeTrialTenant(t, "trial", profilePtr(domain.TrialProfileCustom),
		map[domain.LimitKey]int64{domain.LimitKeyWorkflowTemplateLimit: 7})
	assert.Equal(t, "custom", *es.TrialProfile)
	assert.Equal(t, int64(7), *limitValue(t, es, domain.LimitKeyWorkflowTemplateLimit), "trial override wins")
	assert.Equal(t, tsLLM, *limitValue(t, es, domain.LimitKeyLLMTokenQuotaMonthly), "no override → Trial-Specific base")
}

func TestTrialProfile_NotInTrial_ProfileIgnored(t *testing.T) {
	es := recomputeTrialTenant(t, "active", profilePtr(domain.TrialProfileReducedPlan), nil)
	assert.Nil(t, es.TrialProfile)
	assert.Nil(t, limitValue(t, es, domain.LimitKeyLLMTokenQuotaMonthly))
}

// ── E-8: ENT-O3 reason + AL-5 audit ────────────────────────────────────────

type capturingAuditRecorder struct{ entries []port.AuditEntry }

func (r *capturingAuditRecorder) Record(_ context.Context, e port.AuditEntry) error {
	r.entries = append(r.entries, e)
	return nil
}

func (r *capturingAuditRecorder) RecordBatch(_ context.Context, es []port.AuditEntry) error {
	r.entries = append(r.entries, es...)
	return nil
}

func newTrialServiceWithAudit(rec port.AuditRecorder) *service.TrialService {
	trials := newTrialRepo()
	tx := newTxRunnerWithAuditRecorder(rec)
	effectSvc := service.NewEffectiveSetService(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials,
		&fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	return service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})
}

func assignReq(tenantID, actor uuid.UUID, p domain.TrialProfile, reason string) port.AssignTrialProfileRequest {
	now := time.Now().UTC()
	return port.AssignTrialProfileRequest{
		TenantID: tenantID, PlanCode: "pro", Profile: p,
		StartsAt: now, EndsAt: now.Add(30 * 24 * time.Hour),
		SetBy: actor, Reason: reason,
	}
}

func TestAssignTrialProfile_ReasonRequired(t *testing.T) {
	rec := &capturingAuditRecorder{}
	svc := newTrialServiceWithAudit(rec)
	_, err := svc.AssignTrialProfile(context.Background(), assignReq(uuid.New(), uuid.New(), domain.TrialProfileReducedPlan, ""))
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrReasonRequired)
	assert.Empty(t, rec.entries)
}

func TestAssignTrialProfile_WritesAuditEntry(t *testing.T) {
	rec := &capturingAuditRecorder{}
	svc := newTrialServiceWithAudit(rec)
	tenantID, actor := uuid.New(), uuid.New()

	_, err := svc.AssignTrialProfile(context.Background(), assignReq(tenantID, actor, domain.TrialProfileReducedPlan, "partner evaluation"))
	require.NoError(t, err)
	_, err = svc.AssignTrialProfile(context.Background(), assignReq(tenantID, actor, domain.TrialProfileFullPlan, "sales upgrade"))
	require.NoError(t, err)

	require.Len(t, rec.entries, 2)
	first, second := rec.entries[0], rec.entries[1]
	assert.Equal(t, "config.trial_profile.set", first.EntryType)
	assert.Equal(t, tenantID, *first.TenantID)
	assert.Equal(t, port.AuditActor{Type: "platform_operator", ID: actor.String()}, first.Actor)
	assert.Equal(t, "reduced_plan", first.Metadata["profile"])
	assert.Equal(t, "partner evaluation", first.Metadata["reason"])
	assert.NotContains(t, first.Metadata, "previous_profile")

	assert.Equal(t, "full_plan", second.Metadata["profile"])
	assert.Equal(t, "reduced_plan", second.Metadata["previous_profile"])
	assert.NotEmpty(t, first.IdempotencyKey)
	assert.NotEqual(t, first.IdempotencyKey, second.IdempotencyKey)
}

// ENT-INV-6 must not swallow a trial_profile change whose limits are identical
// (trial_specific → custom with no trial overrides share the same base).
func TestTrialProfile_ProfileOnlyChange_WritesNewSnapshot(t *testing.T) {
	tenantID := uuid.New()
	plan := trialProfilePlan()
	iam := &fakeIAMClient{
		fetchSubFn: func(_ context.Context, id uuid.UUID) (*port.TenantSubscription, error) {
			return sampleSub(id, "enterprise", "trial"), nil
		},
	}
	catalog := &fakeCatalogSvc{getPlanFn: func(_ context.Context, _ string) (*domain.Plan, error) { return plan, nil }}
	trials := newTrialRepo()
	svc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), newAddonRepo(), trials, catalog, iam, nil, newTxRunner())

	first, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	assert.Equal(t, "trial_specific", *first.TrialProfile)

	now := time.Now().UTC()
	require.NoError(t, trials.UpsertAssignment(context.Background(), &domain.TenantTrialAssignment{
		TenantID: tenantID, PlanCode: "enterprise", Profile: domain.TrialProfileCustom,
		StartsAt: now, EndsAt: now.Add(30 * 24 * time.Hour), RecordVersion: 1,
	}))
	second, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	assert.Equal(t, "custom", *second.TrialProfile)
	assert.Greater(t, second.Version, first.Version, "profile-only change must write a new snapshot version")

	third, err := svc.Recompute(context.Background(), tenantID, domain.CauseTrialChange)
	require.NoError(t, err)
	assert.Equal(t, second.Version, third.Version, "unchanged set stays a no-op (ENT-INV-6)")
}
