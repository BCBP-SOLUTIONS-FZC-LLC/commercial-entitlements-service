package unit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

// validateLimitKey is package-private; exercised via service methods.

func TestValidateLimitKey_AllKnownKeys_Valid(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	drawdowns := newDrawdownRepo()
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, drawdowns, shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})

	for _, key := range []domain.LimitKey{
		domain.LimitKeyLLMTokenQuotaMonthly,
		domain.LimitKeyAPIRequestBudgetMonthly,
		domain.LimitKeyAPIRateLimitRPS,
		domain.LimitKeyWorkflowTemplateLimit,
		domain.LimitKeyTenderLimit,
	} {
		req := port.AddAddonRequest{
			TenantID:    uuid.New(),
			LimitKey:    key,
			TotalTokens: 1000,
			GrantedBy:   uuid.New(),
			Reason:      "test",
		}
		_, err := svc.AddAddon(context.Background(), req)
		assert.NoError(t, err, "key %s should be valid", key)
	}
}

func TestValidateLimitKey_UnknownKeys_Error(t *testing.T) {
	snaps := newSnapshotRepo()
	overrides := newOverrideRepo()
	addons := newAddonRepo()
	trials := newTrialRepo()
	tx := newTxRunner()
	effectSvc := service.NewEffectiveSetService(snaps, overrides, addons, trials, &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).WithLogger(port.NopLogger{})
	drawdowns := newDrawdownRepo()
	shortfalls := newShortfallRepo()
	svc := service.NewAddonService(addons, drawdowns, shortfalls, effectSvc, tx).WithLogger(port.NopLogger{})

	for _, key := range []domain.LimitKey{"", "unknown_key", "storage_gb"} {
		req := port.AddAddonRequest{
			TenantID:    uuid.New(),
			LimitKey:    key,
			TotalTokens: 1000,
			GrantedBy:   uuid.New(),
			Reason:      "test",
		}
		_, err := svc.AddAddon(context.Background(), req)
		require.Error(t, err, "key %q should be invalid", key)
		assert.True(t, errors.Is(err, domain.ErrInvalidLimitKey), "key %q: got %v", key, err)
	}
}
