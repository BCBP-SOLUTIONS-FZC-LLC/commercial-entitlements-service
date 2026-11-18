package domain

import (
	"time"

	"github.com/google/uuid"
)

// TrialProfile is the trial profile code key (references Plan.TrialProfiles).
type TrialProfile string

const (
	TrialProfileTrialSpecific TrialProfile = "trial_specific"
	TrialProfileReducedPlan   TrialProfile = "reduced_plan"
	TrialProfileFullPlan      TrialProfile = "full_plan"
	TrialProfileCustom        TrialProfile = "custom"
)

// ValidTrialProfiles is the authoritative set of recognised trial profile codes.
var ValidTrialProfiles = map[TrialProfile]bool{
	TrialProfileTrialSpecific: true,
	TrialProfileReducedPlan:   true,
	TrialProfileFullPlan:      true,
	TrialProfileCustom:        true,
}

// TenantTrialAssignment records the trial period and profile for a tenant.
type TenantTrialAssignment struct {
	TenantID      uuid.UUID
	PlanCode      string
	Profile       TrialProfile
	StartsAt      time.Time
	EndsAt        time.Time
	SetBy         uuid.UUID
	RecordVersion int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TrialEntitlementOverride is a temporary limit override applied only during trial.
// It is separate from operator overrides and is swept when the trial ends.
type TrialEntitlementOverride struct {
	TenantID  uuid.UUID
	LimitKey  LimitKey
	Value     *int64 // nil = unlimited
	SetBy     uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}
