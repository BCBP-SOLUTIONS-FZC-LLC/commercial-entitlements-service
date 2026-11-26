// Package postgres — white-box unit tests for unexported helpers.
// Uses package postgres (not postgres_test) to access unexported symbols.
//
// pgx and pgconn are imported here because pgcommon.Tx is a type alias for
// pgx.Tx. Stubs implementing pgcommon.Tx must satisfy the full pgx.Tx
// interface, whose method signatures use pgconn.CommandTag,
// pgconn.StatementDescription, pgx.Rows, pgx.Batch etc. These imports are
// confined to test files; all production database code uses only pgcommon.
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgmigrate "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/migrate"
	pgcommon "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── TxRunner.RunInTx — join mode ─────────────────────────────────────────────

// stubTx satisfies pgcommon.Tx (= pgx.Tx) with no-op implementations so we
// can store a Tx in context and exercise TxRunner's join-mode path without a
// real database connection.
type stubTx struct{}

func (stubTx) Begin(_ context.Context) (pgx.Tx, error)                    { return nil, nil }
func (stubTx) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) { return nil, nil }
func (stubTx) Commit(_ context.Context) error                             { return nil }
func (stubTx) Rollback(_ context.Context) error                           { return nil }
func (stubTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (stubTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) { return nil, nil }
func (stubTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row        { return nil }
func (stubTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (stubTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults { return nil }
func (stubTx) LargeObjects() pgx.LargeObjects                             { return pgx.LargeObjects{} }
func (stubTx) Prepare(_ context.Context, _, _ string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (stubTx) Conn() *pgx.Conn { return nil }

var _ pgcommon.Tx = stubTx{}

func TestTxRunner_RunInTx_JoinMode_FnCalled(t *testing.T) {
	runner := NewTxRunner(nil, nil)
	ctx := port.WithTx(context.Background(), stubTx{})

	called := false
	err := runner.RunInTx(ctx, func(_ context.Context) error {
		called = true
		return nil
	})
	assert.NoError(t, err)
	assert.True(t, called, "fn must be called in join mode")
}

func TestTxRunner_RunInTx_JoinMode_FnError_Propagates(t *testing.T) {
	runner := NewTxRunner(nil, nil)
	ctx := port.WithTx(context.Background(), stubTx{})

	want := errors.New("fn failed")
	err := runner.RunInTx(ctx, func(_ context.Context) error { return want })
	assert.ErrorIs(t, err, want)
}

func TestTxRunner_RunInTx_JoinMode_InjectsPubAndAudit(t *testing.T) {
	runner := NewTxRunner(nil, nil)
	ctx := port.WithTx(context.Background(), stubTx{})

	var gotCtx context.Context
	_ = runner.RunInTx(ctx, func(c context.Context) error {
		gotCtx = c
		return nil
	})
	// Context is propagated unchanged (no publisher/audit injected when both nil).
	assert.NotNil(t, gotCtx)
}

// ── explainMigrationError ─────────────────────────────────────────────────────

func TestExplainMigrationError_DirtySchema(t *testing.T) {
	err := explainMigrationError("test stage", pgmigrate.ErrMigrationDirty)
	assert.ErrorIs(t, err, pgmigrate.ErrMigrationDirty)
	assert.Contains(t, err.Error(), "repair it by hand")
}

func TestExplainMigrationError_VersionNotInSource(t *testing.T) {
	err := explainMigrationError("test stage", pgmigrate.ErrVersionNotInSource)
	assert.ErrorIs(t, err, pgmigrate.ErrVersionNotInSource)
	assert.Contains(t, err.Error(), "deploy a build")
}

func TestExplainMigrationError_OtherError(t *testing.T) {
	inner := errors.New("some db error")
	err := explainMigrationError("test stage", inner)
	assert.ErrorIs(t, err, inner)
	assert.Contains(t, err.Error(), "test stage")
}

// ── isDBUnavailable / wrapConnErr ─────────────────────────────────────────────

func TestWrapConnErr_NilPassthrough(t *testing.T) {
	assert.NoError(t, wrapConnErr(nil))
}

func TestWrapConnErr_DomainErrorPassthrough(t *testing.T) {
	inner := errors.New("domain problem")
	de := &stubDomainErr{inner: inner}
	got := wrapConnErr(de)
	assert.Same(t, de, got, "domain errors must pass through wrapConnErr unchanged")
}

// stubDomainErr simulates a *domain.DomainError for the wrapConnErr test.
type stubDomainErr struct{ inner error }

func (d *stubDomainErr) Error() string { return d.inner.Error() }

// ── RunInTx join mode — events and audit injection ───────────────────────────

func TestTxRunner_RunInTx_JoinMode_InjectsPublisher(t *testing.T) {
	pub := &txStubPublisher{}
	runner := NewTxRunner(nil, pub)
	ctx := port.WithTx(context.Background(), stubTx{})

	var gotCtx context.Context
	_ = runner.RunInTx(ctx, func(c context.Context) error {
		gotCtx = c
		return nil
	})
	retrieved, ok := port.EventPublisherFromContext(gotCtx)
	assert.True(t, ok, "EventPublisher must be injected into tx context")
	assert.Equal(t, pub, retrieved)
}

func TestTxRunner_RunInTx_JoinMode_InjectsAuditRecorder(t *testing.T) {
	rec := &txStubAudit{}
	runner := NewTxRunner(nil, nil)
	runner.WithAuditRecorder(rec)
	ctx := port.WithTx(context.Background(), stubTx{})

	var gotCtx context.Context
	_ = runner.RunInTx(ctx, func(c context.Context) error {
		gotCtx = c
		return nil
	})
	retrieved, ok := port.AuditRecorderFromContext(gotCtx)
	assert.True(t, ok, "AuditRecorder must be injected into tx context")
	assert.Equal(t, rec, retrieved)
}

// txStubPublisher implements port.EventPublisher with the correct signature.
type txStubPublisher struct{}

func (s *txStubPublisher) Enqueue(_ context.Context, _ *domain.DomainEvent) error { return nil }

// txStubAudit implements port.AuditRecorder.
type txStubAudit struct{}

func (s *txStubAudit) Record(_ context.Context, _ port.AuditEntry) error        { return nil }
func (s *txStubAudit) RecordBatch(_ context.Context, _ []port.AuditEntry) error { return nil }

// ── withPool join mode ────────────────────────────────────────────────────────

func TestWithPool_JoinMode_FnCalledWithTx(t *testing.T) {
	tx := stubTx{}
	ctx := port.WithTx(context.Background(), tx)

	called := false
	err := withPool(ctx, nil, func(got pgcommon.Tx) error {
		called = true
		assert.Equal(t, tx, got)
		return nil
	})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestWithPool_JoinMode_FnError_Propagates(t *testing.T) {
	ctx := port.WithTx(context.Background(), stubTx{})
	want := errors.New("fn error")
	err := withPool(ctx, nil, func(_ pgcommon.Tx) error { return want })
	assert.ErrorIs(t, err, want)
}

// ── errorTx — a stubTx that returns errors from QueryRow/Query/Exec ───────────

type errorRow struct{ err error }

func (r *errorRow) Scan(_ ...any) error { return r.err }

type errorTx struct {
	stubTx
	queryRowErr error
	queryErr    error
	execErr     error
}

func (e *errorTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return &errorRow{err: e.queryRowErr}
}

func (e *errorTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, e.queryErr
}

func (e *errorTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, e.execErr
}

// TestAddonRepository_GetByID_ScanError covers the wrapConnErr path when
// QueryRow.Scan returns a non-ErrNoRows error (injected via errorTx).
func TestAddonRepository_GetByID_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"} // connection exception
	eTx := &errorTx{queryRowErr: scanErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewAddonRepository(nil)
	_, err := repo.GetByID(ctx, domain.UUID{})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable, "scan error must map to ErrDBUnavailable via wrapConnErr")
}

// TestAddonRepository_GetByBillingRef_ScanError covers wrapConnErr in GetByBillingRef.
func TestAddonRepository_GetByBillingRef_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryRowErr: scanErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewAddonRepository(nil)
	_, err := repo.GetByBillingRef(ctx, domain.UUID{}, "ref")
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestAddonRepository_ListByTenant_QueryError covers the query error path in ListByTenant.
func TestAddonRepository_ListByTenant_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewAddonRepository(nil)
	_, err := repo.ListByTenant(ctx, domain.UUID{}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestOverrideRepository_Get_ScanError covers wrapConnErr in Override.Get.
func TestOverrideRepository_Get_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryRowErr: scanErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewOverrideRepository(nil)
	_, err := repo.Get(ctx, domain.UUID{}, domain.LimitKeyTenderLimit)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestOverrideRepository_ListByTenant_QueryError covers query error in ListByTenant.
func TestOverrideRepository_ListByTenant_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewOverrideRepository(nil)
	_, err := repo.ListByTenant(ctx, domain.UUID{})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// ── errorRowsWithScanErr — returns 1 row that fails on Scan ──────────────────

// pgtype needed for TypeMap
// import omitted since pgtype is already a transitive dep via pgconn

type errorRowsOneScanErr struct {
	scanErr error
	done    bool
}

func (r *errorRowsOneScanErr) Close()                                       {}
func (r *errorRowsOneScanErr) Err() error                                   { return nil }
func (r *errorRowsOneScanErr) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *errorRowsOneScanErr) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *errorRowsOneScanErr) Next() bool {
	if r.done {
		return false
	}
	r.done = true
	return true
}
func (r *errorRowsOneScanErr) Scan(_ ...any) error    { return r.scanErr }
func (r *errorRowsOneScanErr) Values() ([]any, error) { return nil, nil }
func (r *errorRowsOneScanErr) RawValues() [][]byte    { return nil }
func (r *errorRowsOneScanErr) Conn() *pgx.Conn        { return nil }
func (r *errorRowsOneScanErr) TypeMap() *pgtype.Map   { return nil }

// queryErrTx returns a custom rows on Query (not an error).
type queryRowsTx struct {
	stubTx
	rows *errorRowsOneScanErr
}

func (q *queryRowsTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return q.rows, nil
}

// TestAddonRepository_CollectAddons_ScanError covers the scan error in collectAddons.
func TestAddonRepository_CollectAddons_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"}
	rows := &errorRowsOneScanErr{scanErr: scanErr}
	qTx := &queryRowsTx{rows: rows}
	ctx := port.WithTx(context.Background(), qTx)

	repo := NewAddonRepository(nil)
	_, err := repo.ListExpired(ctx, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestAddonRepository_Update_ExecError covers the exec error in Update.
func TestAddonRepository_Update_ExecError(t *testing.T) {
	execErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{execErr: execErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewAddonRepository(nil)
	a := &domain.QuotaAddon{ID: domain.UUID{}, TenantID: domain.UUID{}, RecordVersion: 1}
	err := repo.Update(ctx, a)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestOverrideRepository_Upsert_ExecError covers exec error in Upsert.
func TestOverrideRepository_Upsert_ExecError(t *testing.T) {
	execErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{execErr: execErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewOverrideRepository(nil)
	now := time.Now().UTC()
	val := int64(100)
	err := repo.Upsert(ctx, &domain.Override{
		TenantID: domain.UUID{}, LimitKey: domain.LimitKeyTenderLimit,
		Value: &val, SetBy: domain.UUID{}, Reason: "test",
		CreatedAt: now, UpdatedAt: now,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestOverrideRepository_ListByTenant_QueryError covers query error in ListByTenant.
func TestOverrideRepository_ListExpired_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewOverrideRepository(nil)
	_, err := repo.ListExpired(ctx, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestShortfallRepository_Update_ExecError covers exec error in Update.
func TestShortfallRepository_Update_ExecError(t *testing.T) {
	execErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{execErr: execErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewShortfallRepository(nil)
	now := time.Now().UTC()
	err := repo.Update(ctx, &domain.QuotaAddonShortfall{
		ID: domain.UUID{}, TenantID: domain.UUID{},
		Status: domain.ShortfallStatusApproved, UpdatedAt: now,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestShortfallRepository_ListByTenant_QueryError covers query error in ListByTenant.
func TestShortfallRepository_ListByTenant_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewShortfallRepository(nil)
	_, err := repo.ListByTenant(ctx, domain.UUID{}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestAddonRepository_ListByTenant_WithKey_QueryError covers the limitKey!=nil query error.
func TestAddonRepository_ListByTenant_WithKey_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	limitKey := domain.LimitKeyLLMTokenQuotaMonthly
	repo := NewAddonRepository(nil)
	_, err := repo.ListByTenant(ctx, domain.UUID{}, &limitKey)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestAddonRepository_ListExpired_QueryError2 covers query error in ListExpired via errorTx.
func TestAddonRepository_ListExpired_QueryError2(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewAddonRepository(nil)
	_, err := repo.ListExpired(ctx, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestAddonRepository_ListActiveByTenantAndKey_QueryError covers query error in ListActive.
func TestAddonRepository_ListActiveByTenantAndKey_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewAddonRepository(nil)
	_, err := repo.ListActiveByTenantAndKey(ctx, domain.UUID{}, domain.LimitKeyLLMTokenQuotaMonthly)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestDrawdownRepository_ListByTenantAndKey_QueryError covers query error in ListByTenantAndKey.
func TestDrawdownRepository_ListByTenantAndKey_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewDrawdownRepository(nil)
	_, err := repo.ListByTenantAndKey(ctx, domain.UUID{}, domain.LimitKeyLLMTokenQuotaMonthly)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestDrawdownRepository_ListByTenantAndKey_ScanError covers scan error via queryRowsTx.
func TestDrawdownRepository_ListByTenantAndKey_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"}
	rows := &errorRowsOneScanErr{scanErr: scanErr}
	qTx := &queryRowsTx{rows: rows}
	ctx := port.WithTx(context.Background(), qTx)

	repo := NewDrawdownRepository(nil)
	_, err := repo.ListByTenantAndKey(ctx, domain.UUID{}, domain.LimitKeyLLMTokenQuotaMonthly)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestOverrideRepository_ListHistory_QueryError covers query error in ListHistory.
func TestOverrideRepository_ListHistory_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewOverrideRepository(nil)
	_, err := repo.ListHistory(ctx, domain.UUID{}, domain.LimitKeyTenderLimit)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestOverrideRepository_ListHistory_ScanError covers scan error in ListHistory.
func TestOverrideRepository_ListHistory_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"}
	rows := &errorRowsOneScanErr{scanErr: scanErr}
	qTx := &queryRowsTx{rows: rows}
	ctx := port.WithTx(context.Background(), qTx)

	repo := NewOverrideRepository(nil)
	_, err := repo.ListHistory(ctx, domain.UUID{}, domain.LimitKeyTenderLimit)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestTrialRepository_ListOverrides_QueryError covers query error in ListOverrides.
func TestTrialRepository_ListOverrides_QueryError(t *testing.T) {
	queryErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{queryErr: queryErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewTrialRepository(nil)
	_, err := repo.ListOverrides(ctx, domain.UUID{})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestTrialRepository_ListOverrides_ScanError covers scan error in ListOverrides.
func TestTrialRepository_ListOverrides_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"}
	rows := &errorRowsOneScanErr{scanErr: scanErr}
	qTx := &queryRowsTx{rows: rows}
	ctx := port.WithTx(context.Background(), qTx)

	repo := NewTrialRepository(nil)
	_, err := repo.ListOverrides(ctx, domain.UUID{})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestShortfallRepository_ListByTenant_ScanError covers scanShortfallRow error in ListByTenant.
func TestShortfallRepository_ListByTenant_ScanError(t *testing.T) {
	scanErr := &pgconn.PgError{Code: "08006"}
	rows := &errorRowsOneScanErr{scanErr: scanErr}
	qTx := &queryRowsTx{rows: rows}
	ctx := port.WithTx(context.Background(), qTx)

	repo := NewShortfallRepository(nil)
	_, err := repo.ListByTenant(ctx, domain.UUID{}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}

// TestCatalogCacheRepository_SetPlan_ExecError covers exec error in SetPlan.
func TestCatalogCacheRepository_SetPlan_ExecError(t *testing.T) {
	execErr := &pgconn.PgError{Code: "08006"}
	eTx := &errorTx{execErr: execErr}
	ctx := port.WithTx(context.Background(), eTx)

	repo := NewCatalogCacheRepository(nil)
	err := repo.SetPlan(ctx, &domain.Plan{Code: "test", DisplayName: "Test"})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrDBUnavailable)
}
