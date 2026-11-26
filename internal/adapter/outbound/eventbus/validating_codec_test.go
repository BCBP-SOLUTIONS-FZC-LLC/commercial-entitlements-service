package eventbus

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestValidatingCodec(t *testing.T) *ValidatingCodec {
	t.Helper()
	vc, err := NewValidatingCodec(NoopCodec{})
	require.NoError(t, err, "NewValidatingCodec must not fail on embedded schemas")
	return vc
}

// ── NewValidatingCodec ───────────────────────────────────────────────────────

func TestNewValidatingCodec_LoadsEmbeddedSchemas(t *testing.T) {
	vc := newTestValidatingCodec(t)
	// Exactly one schema should be compiled (TenantEntitlementsChanged).
	assert.NotEmpty(t, vc.schemas, "expected at least one compiled schema")
	// The schema key is the event type constant value: snake_case, not PascalCase.
	_, ok := vc.schemas["tenant_entitlements_changed"]
	assert.True(t, ok, "tenant_entitlements_changed schema must be present")
}

// ── Validate ─────────────────────────────────────────────────────────────────

func TestValidate_ValidPayload(t *testing.T) {
	vc := newTestValidatingCodec(t)
	payload := []byte(`{
		"tenant_id":   "3fa85f64-5717-4562-b3fc-2c963f66afa6",
		"version":     1,
		"cause":       "plan_change",
		"computed_at": "2026-11-01T09:00:00Z"
	}`)
	err := vc.Validate("tenant_entitlements_changed", payload)
	assert.NoError(t, err, "valid payload must pass schema validation")
}

func TestValidate_MissingRequiredField(t *testing.T) {
	vc := newTestValidatingCodec(t)
	// Missing required "version" field.
	payload := []byte(`{
		"tenant_id":   "3fa85f64-5717-4562-b3fc-2c963f66afa6",
		"cause":       "plan_change",
		"computed_at": "2026-11-01T09:00:00Z"
	}`)
	err := vc.Validate("tenant_entitlements_changed", payload)
	assert.Error(t, err, "payload missing required 'version' must fail")
}

func TestValidate_InvalidCauseEnum(t *testing.T) {
	vc := newTestValidatingCodec(t)
	payload := []byte(`{
		"tenant_id":   "3fa85f64-5717-4562-b3fc-2c963f66afa6",
		"version":     1,
		"cause":       "not_a_valid_cause",
		"computed_at": "2026-11-01T09:00:00Z"
	}`)
	err := vc.Validate("tenant_entitlements_changed", payload)
	assert.Error(t, err, "unknown enum value for 'cause' must fail validation")
}

func TestValidate_MalformedJSON(t *testing.T) {
	vc := newTestValidatingCodec(t)
	err := vc.Validate("tenant_entitlements_changed", []byte(`{not valid json`))
	assert.Error(t, err, "malformed JSON must fail validation")
}

func TestValidate_UnknownEventType_ReturnsErrNoSchema(t *testing.T) {
	vc := newTestValidatingCodec(t)
	err := vc.Validate("UnknownEventType", []byte(`{}`))
	assert.True(t, errors.Is(err, ErrNoSchema),
		"unknown event type must return ErrNoSchema sentinel")
}

func TestValidate_AdditionalPropertiesAllowed(t *testing.T) {
	vc := newTestValidatingCodec(t)
	// Open-schema: extra fields must be accepted (SCHEMA-1 — additionalProperties: true).
	payload := []byte(`{
		"tenant_id":    "3fa85f64-5717-4562-b3fc-2c963f66afa6",
		"version":      7,
		"cause":        "addon_change",
		"computed_at":  "2026-11-01T09:00:00Z",
		"extra_field":  "forward_compat_value"
	}`)
	err := vc.Validate("tenant_entitlements_changed", payload)
	assert.NoError(t, err, "additionalProperties must be allowed per open-schema contract")
}

// ── Encode delegates to inner codec ─────────────────────────────────────────

func TestEncode_ValidPayloadDelegatestoInner(t *testing.T) {
	vc := newTestValidatingCodec(t)
	payload := []byte(`{
		"tenant_id":   "3fa85f64-5717-4562-b3fc-2c963f66afa6",
		"version":     1,
		"cause":       "catalog_refresh",
		"computed_at": "2026-11-01T09:00:00Z"
	}`)
	encoded, schemaID, err := vc.Encode(context.Background(), "tenant_entitlements_changed", payload)
	require.NoError(t, err)
	assert.Equal(t, payload, encoded, "NoopCodec must pass payload through unchanged")
	assert.Empty(t, schemaID, "NoopCodec schema version ID must be empty")
}

func TestEncode_InvalidPayloadReturnsError(t *testing.T) {
	vc := newTestValidatingCodec(t)
	// Missing required "tenant_id".
	payload := []byte(`{"version": 1, "cause": "plan_change", "computed_at": "2026-11-01T09:00:00Z"}`)
	_, _, err := vc.Encode(context.Background(), "tenant_entitlements_changed", payload)
	assert.Error(t, err, "Encode must reject invalid payload before passing to inner codec")
}

func TestEncode_UnknownEventTypePassesThrough(t *testing.T) {
	vc := newTestValidatingCodec(t)
	// ErrNoSchema → forward-compat: Encode must succeed.
	payload := []byte(`{"any": "data"}`)
	encoded, _, err := vc.Encode(context.Background(), "FutureEventType", payload)
	require.NoError(t, err, "ErrNoSchema must be treated as pass-through")
	assert.Equal(t, payload, encoded)
}
