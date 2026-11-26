// White-box tests for unexported helpers in the postgres adapter.
// Package postgres (not postgres_test) so wrapConnErr and itoa are reachable.
//
// pgconn is imported here solely to construct *pgconn.PgError test fixtures so
// pgcommon.Is* helpers (IsDeadlock, IsConnectionException, etc.) can recognise
// them in wrapConnErr tests. pgcommon.Tx is a type alias for pgx.Tx, so test
// stubs satisfying pgcommon.Tx must implement the full pgx.Tx interface. These
// imports are confined to test files; production code uses only pgcommon.
package postgres

import (
	"context"
	"errors"
	"testing"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── wrapConnErr ──────────────────────────────────────────────────────────────

func TestWrapConnErr_Nil_ReturnsNil(t *testing.T) {
	assert.NoError(t, wrapConnErr(nil))
}

func TestWrapConnErr_DomainError_Passthrough(t *testing.T) {
	de := domain.NewError(domain.ErrSnapshotNotFound, "not found")
	got := wrapConnErr(de)
	assert.Same(t, de, got.(*domain.DomainError),
		"wrapConnErr must not re-wrap an existing DomainError")
}

func TestWrapConnErr_DeadlineExceeded_MapsToRequestTimeout(t *testing.T) {
	err := wrapConnErr(context.DeadlineExceeded)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrRequestTimeout,
		"context.DeadlineExceeded must map to ErrRequestTimeout")
}

func TestWrapConnErr_ContextCanceled_MapsToRequestTimeout(t *testing.T) {
	err := wrapConnErr(context.Canceled)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrRequestTimeout,
		"context.Canceled must map to ErrRequestTimeout")
}

func TestWrapConnErr_UnknownError_Passthrough(t *testing.T) {
	sentinel := errors.New("some_unknown_db_error")
	got := wrapConnErr(sentinel)
	assert.Equal(t, sentinel, got,
		"wrapConnErr must pass through errors it does not recognise")
}

func TestWrapConnErr_Deadlock_MapsToConcurrentUpdate(t *testing.T) {
	err := wrapConnErr(&pgconn.PgError{Code: "40P01"})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrConcurrentUpdate)
}

func TestWrapConnErr_SerializationFailure_MapsToConcurrentUpdate(t *testing.T) {
	err := wrapConnErr(&pgconn.PgError{Code: "40001"})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrConcurrentUpdate)
}

func TestWrapConnErr_QueryCanceled_MapsToRequestTimeout(t *testing.T) {
	err := wrapConnErr(&pgconn.PgError{Code: "57014"})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrRequestTimeout)
}

func TestWrapConnErr_LockNotAvailable_MapsToRequestTimeout(t *testing.T) {
	err := wrapConnErr(&pgconn.PgError{Code: "55P03"})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrRequestTimeout)
}

func TestWrapConnErr_ConnectionException_MapsToDBUnavailable(t *testing.T) {
	// SQLSTATE class "08" = Connection Exception.
	err := wrapConnErr(&pgconn.PgError{Code: "08006"})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// ── itoa ─────────────────────────────────────────────────────────────────────

func TestItoa_PositiveValue_ReturnsString(t *testing.T) {
	assert.Equal(t, "10", itoa(10))
	assert.Equal(t, "1", itoa(1))
	assert.Equal(t, "999", itoa(999))
}

func TestItoa_Zero_ReturnsDefault(t *testing.T) {
	assert.Equal(t, "100", itoa(0),
		"itoa(0) must return the default limit string to prevent SQL LIMIT 0")
}

func TestItoa_Negative_ReturnsDefault(t *testing.T) {
	assert.Equal(t, "100", itoa(-5),
		"itoa(-5) must return the default limit string to prevent invalid SQL")
}

// ── LoggerAdapter / fieldMap ──────────────────────────────────────────────────

func TestLoggerAdapter_Debug_DelegatesWithFieldMap(t *testing.T) {
	log := &captureLogger{}
	a := NewLoggerAdapter(log)
	a.Debug("test message", pgdomain.Field{Key: "k1", Value: "v1"})
	require.Len(t, log.calls, 1)
	assert.Equal(t, "debug", log.calls[0].level)
	assert.Equal(t, "test message", log.calls[0].msg)
	assert.Equal(t, "v1", log.calls[0].fields["k1"])
}

func TestLoggerAdapter_Info_DelegatesWithFieldMap(t *testing.T) {
	log := &captureLogger{}
	a := NewLoggerAdapter(log)
	a.Info("info msg", pgdomain.Field{Key: "key", Value: 42})
	require.Len(t, log.calls, 1)
	assert.Equal(t, "info", log.calls[0].level)
	assert.Equal(t, 42, log.calls[0].fields["key"])
}

func TestLoggerAdapter_Warn_DelegatesWithFieldMap(t *testing.T) {
	log := &captureLogger{}
	a := NewLoggerAdapter(log)
	a.Warn("warn msg", pgdomain.Field{Key: "w", Value: true})
	require.Len(t, log.calls, 1)
	assert.Equal(t, "warn", log.calls[0].level)
}

func TestLoggerAdapter_Error_DelegatesWithFieldMap(t *testing.T) {
	log := &captureLogger{}
	a := NewLoggerAdapter(log)
	a.Error("err msg", pgdomain.Field{Key: "e", Value: "detail"})
	require.Len(t, log.calls, 1)
	assert.Equal(t, "error", log.calls[0].level)
}

func TestLoggerAdapter_NoFields_EmptyMap(t *testing.T) {
	log := &captureLogger{}
	a := NewLoggerAdapter(log)
	a.Debug("no fields")
	require.Len(t, log.calls, 1)
	assert.Empty(t, log.calls[0].fields)
}

func TestLoggerAdapter_MultipleFields_AllPresent(t *testing.T) {
	log := &captureLogger{}
	a := NewLoggerAdapter(log)
	a.Info("multi",
		pgdomain.Field{Key: "a", Value: 1},
		pgdomain.Field{Key: "b", Value: "two"},
		pgdomain.Field{Key: "c", Value: true},
	)
	require.Len(t, log.calls, 1)
	assert.Equal(t, 1, log.calls[0].fields["a"])
	assert.Equal(t, "two", log.calls[0].fields["b"])
	assert.Equal(t, true, log.calls[0].fields["c"])
}

// captureLogger records log calls for assertion.
type captureLogger struct {
	calls []logCall
}

type logCall struct {
	level  string
	msg    string
	fields map[string]any
}

func (l *captureLogger) Debug(msg string, fields map[string]any) {
	l.calls = append(l.calls, logCall{"debug", msg, fields})
}
func (l *captureLogger) Info(msg string, fields map[string]any) {
	l.calls = append(l.calls, logCall{"info", msg, fields})
}
func (l *captureLogger) Warn(msg string, fields map[string]any) {
	l.calls = append(l.calls, logCall{"warn", msg, fields})
}
func (l *captureLogger) Error(msg string, fields map[string]any) {
	l.calls = append(l.calls, logCall{"error", msg, fields})
}

var _ port.Logger = (*captureLogger)(nil)
