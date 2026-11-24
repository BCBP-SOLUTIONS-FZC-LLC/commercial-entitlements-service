package eventbus

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/*.json
var schemasRaw embed.FS

// ErrNoSchema is the sentinel returned when no schema is registered for an event type.
var ErrNoSchema = errNoSchema

var errNoSchema = fmt.Errorf("no schema")

// ValidatingCodec wraps an inner Codec and validates every payload against an
// embedded JSON Schema before forwarding to the inner codec. When no schema is
// registered for an event type, Validate returns ErrNoSchema — callers on the
// consumer path forward-compat through this sentinel.
type ValidatingCodec struct {
	inner    Codec
	schemas  map[string]*jsonschema.Schema // event type → compiled schema
}

// NewValidatingCodec compiles all embedded schemas/*.json and returns a
// ValidatingCodec wrapping inner.
func NewValidatingCodec(inner Codec) (*ValidatingCodec, error) {
	schemas, err := compileSchemas(schemasRaw, "schemas")
	if err != nil {
		return nil, fmt.Errorf("compile event schemas: %w", err)
	}
	return &ValidatingCodec{inner: inner, schemas: schemas}, nil
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
	var doc interface{}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return fmt.Errorf("unmarshal event payload: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

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
		schemaURL := "file:///" + p
		if err := c.AddResource(schemaURL, bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("add schema resource %s: %w", schemaURL, err)
		}
		compiled, err := c.Compile(schemaURL)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", p, err)
		}
		eventType := eventTypeFromSchemaFile(entry.Name())
		schemas[eventType] = compiled
	}
	return schemas, nil
}
