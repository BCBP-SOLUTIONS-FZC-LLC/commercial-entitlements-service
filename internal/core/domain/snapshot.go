package domain

import (
	"time"

	"github.com/google/uuid"
)

// EntitlementSnapshot is the persisted record of a computed EffectiveSet.
// There is exactly one row per tenant; each recomputation replaces it
// (UPSERT) and bumps Version. The version guard prevents lost-update races.
type EntitlementSnapshot struct {
	TenantID            uuid.UUID
	Version             int64
	SourceRecordVersion int64
	Plan                string
	State               EntitlementState
	FeatureFlags        []string
	ModelTier           string
	Limits              map[LimitKey]LimitValue
	TrialProfile        *string
	Cause               EffectiveSetCause
	ComputedAt          time.Time
}
