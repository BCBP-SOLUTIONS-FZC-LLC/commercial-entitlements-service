package unit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

func newTrialService() (*service.TrialService, *fakeTrialRepo, *fakeTxRunner) { //nolint:unparam
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewTrialService(trials, effectSvc, tx).WithLogger(port.NopLogger{})
	return svc, trials, tx
}

// ── AssignTrialProfile ─────────────────────────────────────────────────────

func TestAssignTrialProfile_HappyPath(t *testing.T) {
	svc, trials, _ := newTrialService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	req := port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: tenantID,
		PlanCode: "starter",
		Profile:  domain.TrialProfileReducedPlan,
		StartsAt: now,
		EndsAt:   now.Add(30 * 24 * time.Hour),
		SetBy:    uuid.New(),
	}
	a, err := svc.AssignTrialProfile(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tenantID, a.TenantID)
	assert.Equal(t, domain.TrialProfileReducedPlan, a.Profile)
	assert.Equal(t, int64(1), a.RecordVersion)

	stored, _ := trials.GetAssignment(context.Background(), tenantID)
	assert.Equal(t, domain.TrialProfileReducedPlan, stored.Profile)
}

func TestAssignTrialProfile_VersionIncrements_OnUpdate(t *testing.T) {
	svc, _, _ := newTrialService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	base := port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: tenantID, PlanCode: "starter",
		Profile:  domain.TrialProfileFullPlan,
		StartsAt: now, EndsAt: now.Add(30 * 24 * time.Hour),
		SetBy: uuid.New(),
	}
	a1, _ := svc.AssignTrialProfile(context.Background(), base)
	assert.Equal(t, int64(1), a1.RecordVersion)

	a2, err := svc.AssignTrialProfile(context.Background(), base)
	require.NoError(t, err)
	assert.Equal(t, int64(2), a2.RecordVersion)
}

func TestAssignTrialProfile_InvalidProfile_Error(t *testing.T) {
	svc, _, _ := newTrialService()
	now := time.Now().UTC()
	_, err := svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: uuid.New(), PlanCode: "starter",
		Profile:  "nonexistent_profile",
		StartsAt: now, EndsAt: now.Add(24 * time.Hour),
		SetBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidProfile))
}

func TestAssignTrialProfile_EndsAtZero_Error(t *testing.T) {
	svc, _, _ := newTrialService()
	_, err := svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: uuid.New(), PlanCode: "starter",
		Profile: domain.TrialProfileFullPlan,
		SetBy:   uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrTrialEndsAtInPast))
}

func TestAssignTrialProfile_EndsAtInPast_Error(t *testing.T) {
	svc, _, _ := newTrialService()
	now := time.Now().UTC()
	_, err := svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: uuid.New(), PlanCode: "starter",
		Profile:  domain.TrialProfileFullPlan,
		StartsAt: now.Add(-48 * time.Hour),
		EndsAt:   now.Add(-24 * time.Hour), // in past
		SetBy:    uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrTrialEndsAtInPast))
}

func TestAssignTrialProfile_UpsertError(t *testing.T) {
	svc, trials, _ := newTrialService()
	trials.upsertAssignErr = errStorage
	now := time.Now().UTC()
	_, err := svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: uuid.New(), PlanCode: "starter",
		Profile:  domain.TrialProfileFullPlan,
		StartsAt: now, EndsAt: now.Add(24 * time.Hour),
		SetBy: uuid.New(),
	})
	require.Error(t, err)
}

func TestAssignTrialProfile_AllProfiles(t *testing.T) {
	for _, p := range []domain.TrialProfile{
		domain.TrialProfileTrialSpecific,
		domain.TrialProfileReducedPlan,
		domain.TrialProfileFullPlan,
		domain.TrialProfileCustom,
	} {
		svc, _, _ := newTrialService()
		now := time.Now().UTC()
		a, err := svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
			Reason:   "partner evaluation",
			TenantID: uuid.New(), PlanCode: "starter", Profile: p,
			StartsAt: now, EndsAt: now.Add(24 * time.Hour), SetBy: uuid.New(),
		})
		require.NoError(t, err, "profile %s", p)
		assert.Equal(t, p, a.Profile)
	}
}

// ── GetTrialAssignment ─────────────────────────────────────────────────────

func TestGetTrialAssignment_Found(t *testing.T) {
	svc, _, _ := newTrialService()
	tenantID := uuid.New()
	now := time.Now().UTC()
	_, _ = svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: tenantID, PlanCode: "starter",
		Profile:  domain.TrialProfileFullPlan,
		StartsAt: now, EndsAt: now.Add(24 * time.Hour), SetBy: uuid.New(),
	})

	a, err := svc.GetTrialAssignment(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, tenantID, a.TenantID)
}

func TestGetTrialAssignment_NotFound(t *testing.T) {
	svc, _, _ := newTrialService()
	_, err := svc.GetTrialAssignment(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrTrialAssignmentNotFound))
}

func TestGetTrialAssignment_RepoError_Other(t *testing.T) {
	svc, trials, _ := newTrialService()
	trials.getAssignErr = errStorage
	_, err := svc.GetTrialAssignment(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get trial assignment")
}

// ── SetTrialOverride ───────────────────────────────────────────────────────

func TestSetTrialOverride_HappyPath(t *testing.T) {
	svc, trials, _ := newTrialService()
	tenantID := uuid.New()
	val := int64(99)
	ov, err := svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
		TenantID: tenantID,
		LimitKey: domain.LimitKeyAPIRateLimitRPS,
		Value:    &val,
		SetBy:    uuid.New(),
	})
	require.NoError(t, err)
	assert.Equal(t, tenantID, ov.TenantID)
	assert.Equal(t, val, *ov.Value)

	stored, _ := trials.GetOverride(context.Background(), tenantID, domain.LimitKeyAPIRateLimitRPS)
	assert.Equal(t, val, *stored.Value)
}

func TestSetTrialOverride_UnlimitedValue(t *testing.T) {
	svc, _, _ := newTrialService()
	ov, err := svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyWorkflowTemplateLimit,
		Value: nil, SetBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.Nil(t, ov.Value)
}

func TestSetTrialOverride_InvalidKey_Error(t *testing.T) {
	svc, _, _ := newTrialService()
	_, err := svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
		TenantID: uuid.New(), LimitKey: "bad_key", Value: i64ptr(1), SetBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey))
}

func TestSetTrialOverride_UpsertError(t *testing.T) {
	svc, trials, _ := newTrialService()
	trials.upsertOvErr = errStorage
	_, err := svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, Value: i64ptr(5), SetBy: uuid.New(),
	})
	require.Error(t, err)
}

// ── RemoveTrialOverride ────────────────────────────────────────────────────

func TestRemoveTrialOverride_HappyPath(t *testing.T) {
	svc, trials, _ := newTrialService()
	tenantID := uuid.New()
	_, _ = svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyAPIRateLimitRPS,
		Value: i64ptr(50), SetBy: uuid.New(),
	})

	err := svc.RemoveTrialOverride(context.Background(), port.RemoveTrialOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyAPIRateLimitRPS,
	})
	require.NoError(t, err)

	_, getErr := trials.GetOverride(context.Background(), tenantID, domain.LimitKeyAPIRateLimitRPS)
	assert.True(t, errors.Is(getErr, domain.ErrTrialOverrideNotFound))
}

func TestRemoveTrialOverride_InvalidKey_Error(t *testing.T) {
	svc, _, _ := newTrialService()
	err := svc.RemoveTrialOverride(context.Background(), port.RemoveTrialOverrideRequest{
		TenantID: uuid.New(), LimitKey: "bad_key",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey))
}

// ── ListTrialOverrides ─────────────────────────────────────────────────────

func TestListTrialOverrides_ReturnsAll(t *testing.T) {
	svc, _, _ := newTrialService()
	tenantID := uuid.New()
	for _, key := range []domain.LimitKey{domain.LimitKeyTenderLimit, domain.LimitKeyWorkflowTemplateLimit} {
		_, _ = svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
			TenantID: tenantID, LimitKey: key, Value: i64ptr(5), SetBy: uuid.New(),
		})
	}
	list, err := svc.ListTrialOverrides(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestListTrialOverrides_Empty(t *testing.T) {
	svc, _, _ := newTrialService()
	list, err := svc.ListTrialOverrides(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Empty(t, list)
}

// ── PurgeTrialForTenant ────────────────────────────────────────────────────

func TestPurgeTrialForTenant_RemovesAssignmentAndOverrides(t *testing.T) {
	svc, trials, _ := newTrialService()
	tenantID := uuid.New()
	now := time.Now().UTC()

	// Set assignment
	_, _ = svc.AssignTrialProfile(context.Background(), port.AssignTrialProfileRequest{
		Reason:   "partner evaluation",
		TenantID: tenantID, PlanCode: "starter", Profile: domain.TrialProfileFullPlan,
		StartsAt: now, EndsAt: now.Add(24 * time.Hour), SetBy: uuid.New(),
	})
	// Set overrides
	for _, key := range []domain.LimitKey{domain.LimitKeyTenderLimit, domain.LimitKeyAPIRateLimitRPS} {
		_, _ = svc.SetTrialOverride(context.Background(), port.SetTrialOverrideRequest{
			TenantID: tenantID, LimitKey: key, Value: i64ptr(1), SetBy: uuid.New(),
		})
	}

	err := svc.PurgeTrialForTenant(context.Background(), tenantID)
	require.NoError(t, err)

	_, assignErr := trials.GetAssignment(context.Background(), tenantID)
	assert.True(t, errors.Is(assignErr, domain.ErrTrialAssignmentNotFound))

	ovList, _ := trials.ListOverrides(context.Background(), tenantID)
	assert.Empty(t, ovList)
}

func TestPurgeTrialForTenant_NothingToDelete_NoError(t *testing.T) {
	svc, _, _ := newTrialService()
	err := svc.PurgeTrialForTenant(context.Background(), uuid.New())
	require.NoError(t, err)
}
