package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// E-9: TenantMembershipsPurged forfeits the tenant's add-on balance (CD-55,
// HLD v0.69 P-5a); TenantConverted never does (packs survive conversion, P-5).

func lifecycleEnv(id, eventType string, tenantID uuid.UUID) events.Envelope[json.RawMessage] {
	raw, _ := json.Marshal(map[string]any{"tenant_id": tenantID.String()})
	return events.Envelope[json.RawMessage]{ID: id, Type: eventType, Payload: raw}
}

func TestMembershipConsumer_Purge_ForfeitsAddons(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	addons := &fakeAddonSvc{}
	c := NewMembershipConsumer(nil, eff, &fakeTrialSvc{}, nopConsumerLogger{}).WithAddonForfeiter(addons)
	tenantID := uuid.New()
	require.NoError(t, c.Handle(context.Background(), lifecycleEnv("p1", "TenantMembershipsPurged", tenantID)))
	assert.Equal(t, []uuid.UUID{tenantID}, addons.forfeited)
	assert.Len(t, eff.recomputedWith, 1)
}

func TestMembershipConsumer_Purge_ForfeitError_Retries(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	addons := &fakeAddonSvc{forfeitErr: errors.New("db down")}
	c := NewMembershipConsumer(nil, eff, &fakeTrialSvc{}, nopConsumerLogger{}).WithAddonForfeiter(addons)
	err := c.Handle(context.Background(), lifecycleEnv("p2", "TenantMembershipsPurged", uuid.New()))
	require.Error(t, err, "a failed forfeiture must fail the inbox transaction so the event is redelivered")
	assert.Empty(t, eff.recomputedWith)
}

func TestMembershipConsumer_Purge_NoForfeiterWired_StillRecomputes(t *testing.T) {
	eff := &fakeEffectiveSvc{}
	c := NewMembershipConsumer(nil, eff, &fakeTrialSvc{}, nopConsumerLogger{})
	require.NoError(t, c.Handle(context.Background(), lifecycleEnv("p3", "TenantMembershipsPurged", uuid.New())))
	assert.Len(t, eff.recomputedWith, 1)
}

func TestTenantConsumer_Purge_ForfeitsAddons(t *testing.T) {
	addons := &fakeAddonSvc{}
	c := NewTenantEventConsumer(nil, &fakeEffectiveSvc{}, &fakeTrialSvc{}, nopConsumerLogger{}).WithAddonForfeiter(addons)
	tenantID := uuid.New()
	require.NoError(t, c.Handle(context.Background(), lifecycleEnv("p4", "TenantMembershipsPurged", tenantID)))
	assert.Equal(t, []uuid.UUID{tenantID}, addons.forfeited)
}

func TestTenantConsumer_ConvertedAndOffboarded_DoNotForfeit(t *testing.T) {
	for _, typ := range []string{"TenantConverted", "TenantOffboarded"} {
		addons := &fakeAddonSvc{}
		c := NewTenantEventConsumer(nil, &fakeEffectiveSvc{}, &fakeTrialSvc{}, nopConsumerLogger{}).WithAddonForfeiter(addons)
		require.NoError(t, c.Handle(context.Background(), lifecycleEnv("p5-"+typ, typ, uuid.New())))
		assert.Empty(t, addons.forfeited, "%s must not forfeit add-on packs", typ)
	}
}

// Core may no longer know a purged tenant: the recompute's I-17 404 must not
// roll back the forfeiture (the handler returns nil so the inbox tx commits).
func TestPurge_TenantNotFoundInCore_ForfeitCommits(t *testing.T) {
	addons := &fakeAddonSvc{}
	eff := &fakeEffectiveSvc{recomputeErr: fmt.Errorf("fetch subscription: %w", domain.ErrTenantNotFound)}
	m := NewMembershipConsumer(nil, eff, &fakeTrialSvc{}, nopConsumerLogger{}).WithAddonForfeiter(addons)
	require.NoError(t, m.Handle(context.Background(), lifecycleEnv("nf1", "TenantMembershipsPurged", uuid.New())))

	tc := NewTenantEventConsumer(nil, eff, &fakeTrialSvc{}, nopConsumerLogger{}).WithAddonForfeiter(addons)
	require.NoError(t, tc.Handle(context.Background(), lifecycleEnv("nf2", "TenantMembershipsPurged", uuid.New())))
	assert.Len(t, addons.forfeited, 2)

	// Other recompute errors still fail the transaction.
	eff.recomputeErr = errors.New("iam down")
	require.Error(t, m.Handle(context.Background(), lifecycleEnv("nf3", "TenantMembershipsPurged", uuid.New())))
	// ... and TenantConverted keeps returning not-found errors (not a purge).
	eff.recomputeErr = fmt.Errorf("fetch subscription: %w", domain.ErrTenantNotFound)
	require.Error(t, tc.Handle(context.Background(), lifecycleEnv("nf4", "TenantConverted", uuid.New())))
}
