package auditrecorder

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

type nopAuditLogger struct{}

func (nopAuditLogger) Info(_ string, _ map[string]any)  {}
func (nopAuditLogger) Warn(_ string, _ map[string]any)  {}
func (nopAuditLogger) Error(_ string, _ map[string]any) {}
func (nopAuditLogger) Debug(_ string, _ map[string]any) {}

// ── ErrNoTransaction ──────────────────────────────────────────────────────

func TestRecord_NoTxInContext_ReturnsErrNoTransaction(t *testing.T) {
	r := New(nil)
	err := r.Record(context.Background(), port.AuditEntry{EntryType: "test"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoTransaction)
}

func TestRecordBatch_NoTxInContext_ReturnsErrNoTransaction(t *testing.T) {
	r := New(nil)
	err := r.RecordBatch(context.Background(), []port.AuditEntry{{EntryType: "test"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoTransaction)
}

func TestRecordBatch_Empty_ReturnsNil(t *testing.T) {
	r := New(nil)
	err := r.RecordBatch(context.Background(), nil)
	require.NoError(t, err)
}

func TestRecordBatch_EmptySlice_ReturnsNil(t *testing.T) {
	r := New(nil)
	err := r.RecordBatch(context.Background(), []port.AuditEntry{})
	require.NoError(t, err)
}

// ── toEntry ───────────────────────────────────────────────────────────────

func TestToEntry_AllFields(t *testing.T) {
	tenantID := uuid.New()
	targetID := uuid.New().String()
	e := port.AuditEntry{
		TenantID:        &tenantID,
		EntryType:       "test.event",
		Action:          "create",
		Actor:           port.AuditActor{Type: "operator", ID: "user-1"},
		OccurredAt:      time.Now().UTC(),
		SourceEventType: "TenantPlanChanged",
		IPAddress:       "1.2.3.4",
		UserAgent:       "test-agent/1.0",
		Target:          &port.AuditTarget{Type: "tenant", ID: targetID},
		Metadata:        map[string]any{"plan_code": "starter", "count": 1},
		IdempotencyKey:  "key-1",
	}
	entry, err := toEntry(e)
	require.NoError(t, err)
	assert.Equal(t, "test.event", entry.EntryType)
	assert.Equal(t, "create", entry.Action)
	assert.Equal(t, "operator", string(entry.Actor.Type))
	assert.Equal(t, "user-1", entry.Actor.ID)
	assert.Equal(t, "1.2.3.4", entry.IPAddress)
	assert.Equal(t, "test-agent/1.0", entry.UserAgent)
	assert.NotNil(t, entry.Target)
	assert.Equal(t, targetID, entry.Target.ID)
	assert.NotNil(t, entry.Metadata)
}

func TestToEntry_NilTenantID_ZeroUUID(t *testing.T) {
	e := port.AuditEntry{
		TenantID:  nil,
		EntryType: "test",
		Actor:     port.AuditActor{Type: "system", ID: "srv"},
	}
	entry, err := toEntry(e)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, entry.TenantID)
}

func TestToEntry_NilTarget_NilInOutput(t *testing.T) {
	tenantID := uuid.New()
	e := port.AuditEntry{
		TenantID:  &tenantID,
		EntryType: "test",
		Target:    nil,
	}
	entry, err := toEntry(e)
	require.NoError(t, err)
	assert.Nil(t, entry.Target)
}

func TestToEntry_EmptyMetadata_NilInOutput(t *testing.T) {
	e := port.AuditEntry{EntryType: "test", Metadata: nil}
	entry, err := toEntry(e)
	require.NoError(t, err)
	assert.Nil(t, entry.Metadata)
}

// ── LoadConfig ────────────────────────────────────────────────────────────

func TestLoadConfig_ReadsEnvVars(t *testing.T) {
	t.Setenv("AUDIT_LOG_BASE_URL", "https://audit.example.com")
	t.Setenv("AUDIT_LOG_TIMEOUT_MS", "3000")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://audit.example.com", cfg.BaseURL)
	assert.Equal(t, 3000, cfg.TimeoutMS)
}

func TestLoadConfig_Defaults_WhenEnvUnset(t *testing.T) {
	os.Unsetenv("AUDIT_LOG_BASE_URL")
	os.Unsetenv("AUDIT_LOG_TIMEOUT_MS")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.BaseURL)
	assert.Equal(t, 5000, cfg.TimeoutMS)
}

// ── envInt ────────────────────────────────────────────────────────────────

func TestEnvInt_ParsesValidValue(t *testing.T) {
	t.Setenv("TEST_INT_KEY", "42")
	assert.Equal(t, 42, envInt("TEST_INT_KEY", 10))
}

func TestEnvInt_UsesDefaultForMissingKey(t *testing.T) {
	os.Unsetenv("TEST_INT_MISSING")
	assert.Equal(t, 99, envInt("TEST_INT_MISSING", 99))
}

func TestEnvInt_UsesDefaultForInvalidValue(t *testing.T) {
	t.Setenv("TEST_INT_BAD", "not-a-number")
	assert.Equal(t, 5, envInt("TEST_INT_BAD", 5))
}

func TestEnvInt_UsesDefaultForZeroOrNegative(t *testing.T) {
	t.Setenv("TEST_INT_ZERO", "0")
	assert.Equal(t, 100, envInt("TEST_INT_ZERO", 100))
}

// ── NewClient ─────────────────────────────────────────────────────────────

func TestNewClient_EmptyBaseURL_ReturnsNil(t *testing.T) {
	cfg := Config{BaseURL: "", TimeoutMS: 5000}
	client, err := cfg.NewClient(nopAuditLogger{})
	require.NoError(t, err)
	assert.Nil(t, client)
}

func TestNewClient_WhitespaceBaseURL_ReturnsNil(t *testing.T) {
	cfg := Config{BaseURL: "   ", TimeoutMS: 5000}
	client, err := cfg.NewClient(nopAuditLogger{})
	require.NoError(t, err)
	assert.Nil(t, client)
}

// ── auditLogger ───────────────────────────────────────────────────────────

func TestAuditLogger_AllMethods_NoPanic(t *testing.T) {
	al := auditLogger{l: nopAuditLogger{}}
	fields := map[string]any{"k": "v"}
	assert.NotPanics(t, func() {
		al.Info("info", fields)
		al.Warn("warn", fields)
		al.Error("error", fields)
		al.Debug("debug", fields)
	})
}
