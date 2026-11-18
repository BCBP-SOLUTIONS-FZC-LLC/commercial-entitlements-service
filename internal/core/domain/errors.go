// Package domain holds the core entity types, value objects, and error
// catalogue for the Commercial Entitlements service. It imports nothing outside
// the standard library and third-party value libraries — no framework, no
// adapter, no infrastructure.
package domain

import "errors"

// Sentinel error codes. String value = wire code returned in the response body.
// Kept as errors.New so callers can wrap+unwrap with errors.Is.
var (
	ErrTenantNotFound           = errors.New("tenant_not_found")
	ErrTrialAssignmentNotFound  = errors.New("trial_assignment_not_found")
	ErrTrialAlreadyActive       = errors.New("trial_already_active")
	ErrTrialEndsAtInPast        = errors.New("trial_ends_at_in_past")
	ErrTrialOverrideNotFound    = errors.New("trial_override_not_found")
	ErrUpstreamUnavailable      = errors.New("upstream_unavailable")
	ErrOverrideNotFound         = errors.New("override_not_found")
	ErrOptimisticLockConflict   = errors.New("optimistic_lock_conflict")
	ErrConcurrentUpdate         = errors.New("concurrent_update")
	ErrConflict                 = errors.New("conflict")
	ErrInvalidLimitKey          = errors.New("invalid_limit_key")
	ErrInvalidValue             = errors.New("invalid_value")
	ErrRPSCannotBeUnlimited     = errors.New("rps_cannot_be_unlimited")
	ErrExpiresAtInPast          = errors.New("expires_at_in_past")
	ErrReasonRequired           = errors.New("reason_required")
	ErrInvalidProfile           = errors.New("invalid_profile")
	ErrInvalidAction            = errors.New("invalid_action")
	ErrTokensRequired           = errors.New("tokens_required")
	ErrPackPartiallyUsed        = errors.New("pack_partially_used")
	ErrInvalidPeriodStart       = errors.New("invalid_period_start")
	ErrDBUnavailable            = errors.New("db_unavailable")
	ErrForbidden                = errors.New("forbidden")
	ErrOperatorRoleRequired     = errors.New("operator_role_required")
	ErrDrawdownAlreadyRecorded  = errors.New("drawdown_already_recorded")
	ErrShortfallNotFound        = errors.New("shortfall_not_found")
	ErrShortfallAlreadyResolved = errors.New("shortfall_already_resolved")
	ErrDispositionRefRequired   = errors.New("disposition_ref_required")
	ErrAddonNotFound            = errors.New("addon_not_found")
	ErrPlanNotFound             = errors.New("plan_not_found")
	ErrSnapshotNotFound         = errors.New("snapshot_not_found")
	ErrDependencyUnavailable    = errors.New("dependency_unavailable")
	ErrRequestTimeout           = errors.New("request_timeout")
	ErrCatalogUnavailable       = errors.New("catalog_unavailable")
)

// DomainError wraps a sentinel code with a human-readable message, an
// optional cause chain, and an optional structured details map. The HTTP
// adapter maps Code → HTTP status (see internal/adapter/inbound/http/errors.go).
type DomainError struct {
	Code    string
	Message string
	Cause   error
	// Details is a free-form map serialised into the response body. Used for
	// error-specific context — quota_ceiling on ErrInvalidValue, etc.
	Details map[string]any
}

func (e *DomainError) Error() string { return e.Code + ": " + e.Message }
func (e *DomainError) Unwrap() error { return e.Cause }

// NewError creates a DomainError whose Cause is the given sentinel and whose
// wire Code is the sentinel's string value.
func NewError(sentinel error, message string) *DomainError {
	return &DomainError{Code: sentinel.Error(), Message: message, Cause: sentinel}
}

// WithDetails attaches structured detail fields to the error body. Chainable.
func (e *DomainError) WithDetails(d map[string]any) *DomainError {
	e.Details = d
	return e
}
