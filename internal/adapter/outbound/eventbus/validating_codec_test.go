package eventbus

import (
	"context"
	"errors"
	"strings"
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

// ── ValidateConsumed (B-4, BL-OQ-28) ─────────────────────────────────────────

func TestValidateConsumed_AddonEvent_EnforcesSchema(t *testing.T) {
	vc, err := NewValidatingCodec(NoopCodec{})
	require.NoError(t, err)
	valid := []byte(`{"tenant_id":"9b2f0c1e-2f6a-4a51-9d55-0d4f1c3e8a11","resource":"llm_tokens","delta":500000,` +
		`"reason":"purchase","billing_ref":"pur_1","purchased_at":"2026-11-27T10:00:00Z","pack_code":"tokens_500k"}`)
	assert.NoError(t, vc.ValidateConsumed("billing-entitlements-q", "TenantQuotaAddOnChanged", valid))

	for name, body := range map[string]string{
		"zero delta":          `{"tenant_id":"9b2f0c1e-2f6a-4a51-9d55-0d4f1c3e8a11","resource":"llm_tokens","delta":0,"reason":"purchase","billing_ref":"x"}`,
		"missing billing_ref": `{"tenant_id":"9b2f0c1e-2f6a-4a51-9d55-0d4f1c3e8a11","resource":"llm_tokens","delta":5,"reason":"purchase"}`,
		"unknown reason":      `{"tenant_id":"9b2f0c1e-2f6a-4a51-9d55-0d4f1c3e8a11","resource":"llm_tokens","delta":5,"reason":"gift","billing_ref":"x"}`,
	} {
		err := vc.ValidateConsumed("billing-entitlements-q", "TenantQuotaAddOnChanged", []byte(body))
		assert.Error(t, err, name)
		assert.False(t, errors.Is(err, ErrNoSchema), name)
	}
}

// TenantReactivated has two producers with different contracts; the schema
// is chosen by queue.
func TestValidateConsumed_TenantReactivated_ScopedByQueue(t *testing.T) {
	vc, err := NewValidatingCodec(NoopCodec{})
	require.NoError(t, err)
	rpPayload := []byte(`{"tenant_id":"9b2f0c1e-2f6a-4a51-9d55-0d4f1c3e8a11","realm":"r","source":"operator"}`)
	assert.NoError(t, vc.ValidateConsumed("tenant-entitlements-q", "TenantReactivated", rpPayload))
	assert.Error(t, vc.ValidateConsumed("billing-entitlements-q", "TenantReactivated", rpPayload),
		"Billing's TenantReactivated requires source=billing and reactivated_at")

	billingPayload := []byte(`{"tenant_id":"9b2f0c1e-2f6a-4a51-9d55-0d4f1c3e8a11","source":"billing","reactivated_at":"2026-11-27T10:00:00Z"}`)
	assert.NoError(t, vc.ValidateConsumed("billing-entitlements-q", "TenantReactivated", billingPayload))
}

func TestValidateConsumed_BillingSubscriptionEvents(t *testing.T) {
	vc, err := NewValidatingCodec(NoopCodec{})
	require.NoError(t, err)
	const tid = `"tenant_id":"9b2f0c1e-2f6a-4a51-9d55-0d4f1c3e8a11"`
	assert.NoError(t, vc.ValidateConsumed("billing-entitlements-q", "TenantPlanChanged",
		[]byte(`{`+tid+`,"plan":"pro","previous_plan":"starter","effective_at":"2026-11-27T10:00:00Z","billing_ref":"sub_1"}`)))
	assert.Error(t, vc.ValidateConsumed("billing-entitlements-q", "TenantPlanChanged",
		[]byte(`{`+tid+`,"plan":"platinum","previous_plan":"starter","effective_at":"2026-11-27T10:00:00Z","billing_ref":"sub_1"}`)))
	assert.NoError(t, vc.ValidateConsumed("billing-entitlements-q", "TenantSubscriptionCancelled",
		[]byte(`{`+tid+`,"cancelled_at":"2026-11-27T10:00:00Z","reason":"customer"}`)))
	assert.Error(t, vc.ValidateConsumed("billing-entitlements-q", "TenantSubscriptionCancelled", []byte(`{`+tid+`}`)))
}

func TestValidateConsumed_UnregisteredType_ErrNoSchema(t *testing.T) {
	vc, err := NewValidatingCodec(NoopCodec{})
	require.NoError(t, err)
	assert.ErrorIs(t, vc.ValidateConsumed("billing-entitlements-q", "SomeFutureEvent", []byte(`{}`)), ErrNoSchema)
	assert.ErrorIs(t, vc.ValidateConsumed("unknown-q", "TenantPlanChanged", []byte(`{}`)), ErrNoSchema)
}

// Every embedded inbound_*.json is used by at least one queue, so a schema
// cannot be added and silently never applied again (the original B-4 bug).
func TestConsumedSchemaFiles_EveryInboundSchemaIsMapped(t *testing.T) {
	used := map[string]bool{}
	for _, types := range consumedSchemaFiles {
		for _, f := range types {
			used[f] = true
		}
	}
	entries, err := schemasRaw.ReadDir("schemas")
	require.NoError(t, err)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "inbound_") {
			assert.True(t, used[e.Name()], "%s is embedded but no queue validates against it", e.Name())
		}
	}
}
