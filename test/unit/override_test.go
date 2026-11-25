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

func newOverrideService() (*service.OverrideService, *fakeOverrideRepo, *fakeTxRunner) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})
	return svc, overrides, tx
}

// ── SetOverride ────────────────────────────────────────────────────────────

func TestSetOverride_HappyPath_NewOverride(t *testing.T) {
	svc, overrides, _ := newOverrideService()
	tenantID := uuid.New()
	val := int64(500)
	req := port.SetOverrideRequest{
		TenantID: tenantID,
		LimitKey: domain.LimitKeyTenderLimit,
		Value:    &val,
		Reason:   "special customer",
		SetBy:    uuid.New(),
	}
	ov, err := svc.SetOverride(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, tenantID, ov.TenantID)
	assert.Equal(t, val, *ov.Value)
	assert.Equal(t, int64(1), ov.RecordVersion)

	stored, _ := overrides.Get(context.Background(), tenantID, domain.LimitKeyTenderLimit)
	assert.Equal(t, val, *stored.Value)

	// history entry created
	hist, _ := overrides.ListHistory(context.Background(), tenantID, domain.LimitKeyTenderLimit)
	require.Len(t, hist, 1)
	assert.Equal(t, domain.ActionSet, hist[0].Action)
	assert.Nil(t, hist[0].OldValue)
}

func TestSetOverride_UpdateExistingOverride(t *testing.T) {
	svc, overrides, _ := newOverrideService()
	tenantID := uuid.New()
	actor := uuid.New()

	// Create initial override
	val1 := int64(100)
	_, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: &val1, Reason: "first", SetBy: actor,
	})
	require.NoError(t, err)

	// Update it
	val2 := int64(200)
	ov2, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: &val2, Reason: "updated", SetBy: actor,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), ov2.RecordVersion)
	assert.Equal(t, val2, *ov2.Value)

	hist, _ := overrides.ListHistory(context.Background(), tenantID, domain.LimitKeyTenderLimit)
	assert.Len(t, hist, 2)
	assert.Equal(t, &val1, hist[1].OldValue)
}

func TestSetOverride_UnlimitedValue(t *testing.T) {
	svc, _, _ := newOverrideService()
	ov, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
		Value: nil, Reason: "unlimited", SetBy: uuid.New(),
	})
	require.NoError(t, err)
	assert.True(t, domain.LimitValue{Value: ov.Value}.IsUnlimited())
}

func TestSetOverride_InvalidKey_Error(t *testing.T) {
	svc, _, _ := newOverrideService()
	_, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: uuid.New(), LimitKey: "bad_key",
		Value: i64ptr(1), Reason: "test", SetBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey))
}

func TestSetOverride_ReasonEmpty_Error(t *testing.T) {
	svc, _, _ := newOverrideService()
	_, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(1), Reason: "", SetBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrReasonRequired))
}

func TestSetOverride_ExpiresAtInPast_Error(t *testing.T) {
	svc, _, _ := newOverrideService()
	past := time.Now().UTC().Add(-time.Hour)
	_, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(1), Reason: "test", SetBy: uuid.New(), ExpiresAt: &past,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrExpiresAtInPast))
}

func TestSetOverride_WithFutureExpiry(t *testing.T) {
	svc, _, _ := newOverrideService()
	future := time.Now().UTC().Add(24 * time.Hour)
	ov, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyWorkflowTemplateLimit,
		Value: i64ptr(10), Reason: "trial boost", SetBy: uuid.New(), ExpiresAt: &future,
	})
	require.NoError(t, err)
	require.NotNil(t, ov.ExpiresAt)
}

func TestSetOverride_UpsertError(t *testing.T) {
	svc, overrides, _ := newOverrideService()
	overrides.upsertErr = errStorage
	_, err := svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(1), Reason: "test", SetBy: uuid.New(),
	})
	require.Error(t, err)
}

// ── RemoveOverride ─────────────────────────────────────────────────────────

func TestRemoveOverride_HappyPath(t *testing.T) {
	svc, overrides, _ := newOverrideService()
	tenantID := uuid.New()
	actor := uuid.New()

	// First set an override
	_, _ = svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(50), Reason: "set", SetBy: actor,
	})

	err := svc.RemoveOverride(context.Background(), port.RemoveOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		RemovedBy: actor, Reason: "cleanup",
	})
	require.NoError(t, err)

	_, getErr := overrides.Get(context.Background(), tenantID, domain.LimitKeyTenderLimit)
	assert.True(t, errors.Is(getErr, domain.ErrOverrideNotFound))

	hist, _ := overrides.ListHistory(context.Background(), tenantID, domain.LimitKeyTenderLimit)
	assert.Equal(t, domain.ActionRemoved, hist[len(hist)-1].Action)
}

func TestRemoveOverride_InvalidKey_Error(t *testing.T) {
	svc, _, _ := newOverrideService()
	err := svc.RemoveOverride(context.Background(), port.RemoveOverrideRequest{
		TenantID: uuid.New(), LimitKey: "bad_key", RemovedBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey))
}

func TestRemoveOverride_NotFound_Error(t *testing.T) {
	svc, _, _ := newOverrideService()
	err := svc.RemoveOverride(context.Background(), port.RemoveOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, RemovedBy: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrOverrideNotFound))
}

func TestRemoveOverride_GetError_Other(t *testing.T) {
	svc, overrides, _ := newOverrideService()
	overrides.getErr = errStorage
	err := svc.RemoveOverride(context.Background(), port.RemoveOverrideRequest{
		TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, RemovedBy: uuid.New(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get override")
}

// ── GetOverride ────────────────────────────────────────────────────────────

func TestGetOverride_Found(t *testing.T) {
	svc, _, _ := newOverrideService()
	tenantID := uuid.New()
	_, _ = svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyAPIRateLimitRPS,
		Value: i64ptr(50), Reason: "rate limit", SetBy: uuid.New(),
	})

	ov, err := svc.GetOverride(context.Background(), tenantID, domain.LimitKeyAPIRateLimitRPS)
	require.NoError(t, err)
	assert.Equal(t, int64(50), *ov.Value)
}

func TestGetOverride_NotFound(t *testing.T) {
	svc, _, _ := newOverrideService()
	_, err := svc.GetOverride(context.Background(), uuid.New(), domain.LimitKeyTenderLimit)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrOverrideNotFound))
}

func TestGetOverride_RepoError(t *testing.T) {
	svc, overrides, _ := newOverrideService()
	overrides.getErr = errStorage
	_, err := svc.GetOverride(context.Background(), uuid.New(), domain.LimitKeyTenderLimit)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get override")
}

// ── ListOverrides ──────────────────────────────────────────────────────────

func TestListOverrides_ReturnsAll(t *testing.T) {
	svc, _, _ := newOverrideService()
	tenantID := uuid.New()
	for _, key := range []domain.LimitKey{domain.LimitKeyTenderLimit, domain.LimitKeyWorkflowTemplateLimit} {
		_, _ = svc.SetOverride(context.Background(), port.SetOverrideRequest{
			TenantID: tenantID, LimitKey: key,
			Value: i64ptr(10), Reason: "test", SetBy: uuid.New(),
		})
	}
	list, err := svc.ListOverrides(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

// ── ListOverrideHistory ────────────────────────────────────────────────────

func TestListOverrideHistory_ReturnsHistory(t *testing.T) {
	svc, _, _ := newOverrideService()
	tenantID := uuid.New()
	actor := uuid.New()
	_, _ = svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(5), Reason: "first", SetBy: actor,
	})
	_, _ = svc.SetOverride(context.Background(), port.SetOverrideRequest{
		TenantID: tenantID, LimitKey: domain.LimitKeyTenderLimit,
		Value: i64ptr(10), Reason: "second", SetBy: actor,
	})

	hist, err := svc.ListOverrideHistory(context.Background(), tenantID, domain.LimitKeyTenderLimit)
	require.NoError(t, err)
	assert.Len(t, hist, 2)
}

// ── ExpireOverrides ────────────────────────────────────────────────────────

func TestExpireOverrides_NoExpired(t *testing.T) {
	svc, _, _ := newOverrideService()
	count, err := svc.ExpireOverrides(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestExpireOverrides_ExpiresOne(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})

	tenantID := uuid.New()
	past := time.Now().UTC().Add(-time.Hour)
	overrides.expiredList = []domain.Override{
		{
			TenantID:  tenantID,
			LimitKey:  domain.LimitKeyTenderLimit,
			Value:     i64ptr(5),
			Reason:    "expired",
			SetBy:     uuid.New(),
			ExpiresAt: &past,
		},
	}
	// Also insert into the map so Delete can find it
	_ = overrides.Upsert(context.Background(), &overrides.expiredList[0])

	count, err := svc.ExpireOverrides(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	hist, _ := overrides.ListHistory(context.Background(), tenantID, domain.LimitKeyTenderLimit)
	require.Len(t, hist, 1)
	assert.Equal(t, domain.ActionExpired, hist[0].Action)
}

func TestExpireOverrides_DeleteError_ContinuesOtherEntries(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	svc := service.NewOverrideService(overrides, effectSvc, tx).WithLogger(port.NopLogger{})

	past := time.Now().UTC().Add(-time.Hour)
	overrides.expiredList = []domain.Override{
		{TenantID: uuid.New(), LimitKey: domain.LimitKeyTenderLimit, Value: i64ptr(1), Reason: "x", SetBy: uuid.New(), ExpiresAt: &past},
	}
	overrides.deleteErr = errStorage // delete fails

	count, err := svc.ExpireOverrides(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, count) // failed, not counted
}
