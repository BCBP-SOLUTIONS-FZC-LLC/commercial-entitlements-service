package eventbus

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/*.json
var schemasRaw embed.FS

// ErrNoSchema is the sentinel returned when no schema is registered for an
// event type. Using errors.New ensures errors.Is identity checks work
// correctly across the eventbus package.
var ErrNoSchema = errors.New("no schema")

// ValidatingCodec wraps an inner Codec and validates every payload against an
// embedded JSON Schema before forwarding to the inner codec. When no schema is
// registered for an event type, Validate returns ErrNoSchema — callers on the
// consumer path forward-compat through this sentinel.
type ValidatingCodec struct {
	inner    Codec
	schemas  map[string]*jsonschema.Schema            // event type → compiled schema
	consumed map[string]map[string]*jsonschema.Schema // queue → event type → compiled schema
}

// consumedSchemaFiles maps each consuming queue to the embedded inbound schema
// file for every event type it validates (B-4, BL-OQ-28). It is keyed by
// queue, not only by event type, because one type can arrive from two
// producers with different contracts: TenantReactivated comes from Billing on
// billing-entitlements-q (source: "billing") and from iam-realm-provisioner on
// tenant-entitlements-q. The queue names are the consumer names wired in
// cmd/server/main.go (consumer.*ConsumerName).
var consumedSchemaFiles = map[string]map[string]string{
	"billing-entitlements-q": {
		"TenantQuotaAddOnChanged":     "inbound_tenant_quota_add_on_changed.json",
		"TenantPlanChanged":           "inbound_billing_tenant_plan_changed.json",
		"TenantSubscriptionCancelled": "inbound_billing_tenant_subscription_cancelled.json",
		"TenantReactivated":           "inbound_billing_tenant_reactivated.json",
	},
	"tenant-entitlements-q": {
		"TenantIdentityProvisioned": "inbound_tenant_lifecycle.json",
		"TenantSubscriptionUpdated": "inbound_tenant_lifecycle.json",
		"TenantSuspended":           "inbound_tenant_lifecycle.json",
		"TenantReactivated":         "inbound_tenant_lifecycle.json",
		"TrialExpired":              "inbound_tenant_lifecycle.json",
		"TenantMembershipsPurged":   "inbound_tenant_lifecycle.json",
		"TenantConverted":           "inbound_tenant_lifecycle.json",
		"TenantOffboarded":          "inbound_tenant_lifecycle.json",
	},
	"membership-entitlements-q": {
		"TenantStateChanged":      "inbound_tenant_state_changed.json",
		"TenantMembershipsPurged": "inbound_tenant_lifecycle.json",
	},
}

// NewValidatingCodec compiles all embedded schemas/*.json and returns a
// ValidatingCodec wrapping inner.
func NewValidatingCodec(inner Codec) (*ValidatingCodec, error) {
	byFile, err := compileSchemas(schemasRaw, "schemas")
	if err != nil {
		return nil, fmt.Errorf("compile event schemas: %w", err)
	}
	schemas := make(map[string]*jsonschema.Schema, len(byFile))
	for file, s := range byFile {
		schemas[eventTypeFromSchemaFile(file)] = s
	}
	consumed := make(map[string]map[string]*jsonschema.Schema, len(consumedSchemaFiles))
	for queue, types := range consumedSchemaFiles {
		consumed[queue] = make(map[string]*jsonschema.Schema, len(types))
		for eventType, file := range types {
			s, ok := byFile[file]
			if !ok {
				return nil, fmt.Errorf("consumed schema %s for %s on %s is not embedded", file, eventType, queue)
			}
			consumed[queue][eventType] = s
		}
	}
	return &ValidatingCodec{inner: inner, schemas: schemas, consumed: consumed}, nil
}

// ValidateConsumed validates an inbound payload against the schema registered
// for (queue, eventType). It returns ErrNoSchema when none is registered, so
// an event type added by a producer before this service knows it stays
// forward-compatible.
func (v *ValidatingCodec) ValidateConsumed(queue, eventType string, payload []byte) error {
	schema, ok := v.consumed[queue][eventType]
	if !ok {
		return ErrNoSchema
	}
	return validateAgainst(schema, payload)
}

// Encode validates the payload against the schema (if any) and then delegates
// to the inner codec.
func (v *ValidatingCodec) Encode(ctx context.Context, schemaName string, payload []byte) ([]byte, string, error) {
	if err := v.Validate(schemaName, payload); err != nil && err != ErrNoSchema {
		return nil, "", fmt.Errorf("event payload failed schema validation for %q: %w", schemaName, err)
	}
	return v.inner.Encode(ctx, schemaName, payload)
}

// Validate validates payload against the schema for eventType, returning
// ErrNoSchema when no schema is registered for that type.
func (v *ValidatingCodec) Validate(eventType string, payload []byte) error {
	schema, ok := v.schemas[eventType]
	if !ok {
		return ErrNoSchema
	}
	return validateAgainst(schema, payload)
}

func validateAgainst(schema *jsonschema.Schema, payload []byte) error {
	var doc interface{}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return fmt.Errorf("unmarshal event payload: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

// compileSchemas compiles every embedded schema file, keyed by file name.
func compileSchemas(fsys embed.FS, dir string) (map[string]*jsonschema.Schema, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	schemas := make(map[string]*jsonschema.Schema, len(entries))
	c := jsonschema.NewCompiler()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		p := path.Join(dir, entry.Name())
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("read embedded schema %s: %w", p, err)
		}
		// jsonschema/v6 AddResource expects a decoded JSON value (any), not an
		// io.Reader. Unmarshal first so the library receives the right type.
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse embedded schema %s: %w", p, err)
		}
		schemaURL := "file:///" + p
		if err := c.AddResource(schemaURL, doc); err != nil {
			return nil, fmt.Errorf("add schema resource %s: %w", schemaURL, err)
		}
		compiled, err := c.Compile(schemaURL)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", p, err)
		}
		schemas[entry.Name()] = compiled
	}
	return schemas, nil
}
