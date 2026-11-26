// Package domain_test holds in-package tests for cases NOT covered by
// test/unit/domain_test.go. Run any in-package test to verify these without
// Docker.
package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// TestLimitValue_IsUnlimited_ZeroValue verifies that a pointer to int64(0) is
// NOT unlimited — zero is a valid bounded quota (test/unit only tests nil and
// non-zero).
func TestLimitValue_IsUnlimited_ZeroValue(t *testing.T) {
	v := int64(0)
	lv := LimitValue{Value: &v}
	assert.False(t, lv.IsUnlimited(),
		"zero is a bounded (non-unlimited) value; only nil pointer means unlimited")
}

// TestDomainError_NilCause_UnwrapNil checks that Unwrap returns nil when no
// Cause is set (test/unit only exercises non-nil Cause).
func TestDomainError_NilCause_UnwrapNil(t *testing.T) {
	err := &DomainError{Code: "some_code", Message: "msg"}
	assert.Nil(t, err.Unwrap(),
		"DomainError.Unwrap must return nil when Cause is not set")
}

// TestNewError_CodeEqualsSentinelString verifies that the Code field of a
// DomainError is exactly the string representation of the sentinel error —
// the HTTP layer relies on Code for status mapping.
func TestNewError_CodeEqualsSentinelString(t *testing.T) {
	sentinels := []error{
		ErrSnapshotNotFound,
		ErrAddonNotFound,
		ErrOverrideNotFound,
		ErrOptimisticLockConflict,
		ErrBillingRefAlreadyClaimed,
		ErrInvalidLimitKey,
		ErrShortfallNotFound,
		ErrTrialAssignmentNotFound,
	}
	for _, sentinel := range sentinels {
		e := NewError(sentinel, "test message")
		assert.Equal(t, sentinel.Error(), e.Code,
			"Code must equal sentinel.Error() so the HTTP layer can map it; sentinel=%q", sentinel)
	}
}

// TestNewError_ErrorsIsWorksAfterWrapping verifies that the standard
// errors.Is chain works for all code paths that wrap sentinels.
func TestNewError_ErrorsIsWorksAfterWrapping(t *testing.T) {
	inner := errors.New("inner_cause")
	e := &DomainError{Code: "some_code", Message: "msg", Cause: inner}
	require.True(t, errors.Is(e, inner),
		"errors.Is must unwrap through DomainError.Cause")
}

// TestNewEvent_OccurredAt_Recent verifies that NewEvent stamps OccurredAt with
// the current wall clock — not a zero time (test/unit checks Type/Data but not
// the timestamp).
func TestNewEvent_OccurredAt_Recent(t *testing.T) {
	before := time.Now()
	evt := NewEvent("some_event", nil)
	assert.True(t, !evt.OccurredAt.IsZero(),
		"OccurredAt must not be zero")
	assert.True(t, evt.OccurredAt.After(before) || evt.OccurredAt.Equal(before),
		"OccurredAt must be >= the time just before NewEvent was called")
}

// TestDomainError_WithDetails_Chainable verifies that WithDetails returns the
// same pointer and that subsequent chaining works.
func TestDomainError_WithDetails_Chainable(t *testing.T) {
	e := NewError(ErrInvalidValue, "value out of range")
	returned := e.WithDetails(map[string]any{"max": int64(1000), "got": int64(9999)})
	assert.Same(t, e, returned, "WithDetails must return the receiver for fluent chaining")
	assert.Equal(t, int64(1000), e.Details["max"])
}
