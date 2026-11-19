package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

const (
	glueHeaderVersion byte = 0x03
	glueNoCompression byte = 0x00
	glueHeaderSize         = 18
)

// GlueCodec implements events.Codec using the AWS Glue Schema Registry wire format.
// Version resolution is by content (GetSchemaByDefinition) using this binary's
// own embedded schemas/*.json — stamped with the version this binary actually
// produces, never merely the registry's latest.
type GlueCodec struct {
	client       *glue.Client
	registryName string
	mu           sync.RWMutex
	versionCache map[string]string // schema name → schema version UUID string
}

var _ events.Codec = (*GlueCodec)(nil)

// NewGlueCodec creates a GlueCodec and resolves, for each name in
// schemaNames, the Glue schema version whose definition matches this
// binary's embedded schema file. Fails fast at startup if any definition
// isn't registered (and AVAILABLE) in registryName.
func NewGlueCodec(ctx context.Context, client *glue.Client, registryName string, schemaNames []string) (*GlueCodec, error) {
	c := &GlueCodec{
		client:       client,
		registryName: registryName,
		versionCache: make(map[string]string, len(schemaNames)),
	}
	for _, name := range schemaNames {
		id, err := c.fetchVersionID(ctx, name)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve glue schema %q in registry %q: %w — "+
					"register it via `make schema-register` or wait for schema-registry.yml",
				name, registryName, err,
			)
		}
		c.versionCache[name] = id
	}
	return c, nil
}

// Encode prepends the 18-byte Glue wire-format header to payload.
func (g *GlueCodec) Encode(ctx context.Context, schemaName string, payload json.RawMessage) ([]byte, string, error) {
	versionID, err := g.versionID(ctx, schemaName)
	if err != nil {
		return nil, "", fmt.Errorf("get glue schema version %q: %w", schemaName, err)
	}
	out, err := prependGlueHeader(versionID, payload)
	if err != nil {
		return nil, "", err
	}
	return out, versionID, nil
}

// Decode strips the 18-byte Glue wire-format header and returns the plain-JSON payload.
func (g *GlueCodec) Decode(_ context.Context, _ string, encoded []byte) (json.RawMessage, error) {
	if len(encoded) < glueHeaderSize {
		return nil, fmt.Errorf("glue codec: encoded payload too short (%d bytes < %d header)", len(encoded), glueHeaderSize)
	}
	if encoded[0] != glueHeaderVersion {
		return nil, fmt.Errorf("glue codec: unexpected header byte 0x%02x", encoded[0])
	}
	return json.RawMessage(encoded[glueHeaderSize:]), nil
}

func (g *GlueCodec) versionID(ctx context.Context, schemaName string) (string, error) {
	g.mu.RLock()
	id, ok := g.versionCache[schemaName]
	g.mu.RUnlock()
	if ok {
		return id, nil
	}
	id, err := g.fetchVersionID(ctx, schemaName)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	g.versionCache[schemaName] = id
	g.mu.Unlock()
	return id, nil
}

func (g *GlueCodec) fetchVersionID(ctx context.Context, schemaName string) (string, error) {
	definition, err := registeredDefinition(schemasRaw, schemaName)
	if err != nil {
		return "", err
	}
	regName := g.registryName
	out, err := g.client.GetSchemaByDefinition(ctx, &glue.GetSchemaByDefinitionInput{
		SchemaId: &gluetypes.SchemaId{
			SchemaName:   &schemaName,
			RegistryName: &regName,
		},
		SchemaDefinition: &definition,
	})
	if err != nil {
		return "", err
	}
	if out.SchemaVersionId == nil {
		return "", fmt.Errorf("nil SchemaVersionId for schema %q in registry %q", schemaName, g.registryName)
	}
	if out.Status != gluetypes.SchemaVersionStatusAvailable {
		return "", fmt.Errorf("schema %q version %s in registry %q is %s, not AVAILABLE",
			schemaName, *out.SchemaVersionId, g.registryName, out.Status)
	}
	return *out.SchemaVersionId, nil
}

func registeredDefinition(fsys fs.FS, schemaName string) (string, error) {
	entries, err := fs.ReadDir(fsys, "schemas")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if eventTypeFromSchemaFile(e.Name()) != schemaName {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join("schemas", e.Name()))
		if err != nil {
			return "", fmt.Errorf("read embedded schema %s: %w", e.Name(), err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return "", fmt.Errorf("compact schema %s: %w", e.Name(), err)
		}
		return asciiEscape(compact.Bytes()), nil
	}
	return "", fmt.Errorf("no embedded schema file for %q", schemaName)
}

func asciiEscape(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		b = b[size:]
		switch {
		case r < utf8.RuneSelf:
			out.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&out, "\\u%04x\\u%04x", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return out.String()
}

func prependGlueHeader(schemaVersionID string, payload []byte) ([]byte, error) {
	id, err := uuid.Parse(schemaVersionID)
	if err != nil {
		return nil, fmt.Errorf("parse schema version UUID %q: %w", schemaVersionID, err)
	}
	out := make([]byte, glueHeaderSize+len(payload))
	out[0] = glueHeaderVersion
	out[1] = glueNoCompression
	copy(out[2:18], id[:])
	copy(out[glueHeaderSize:], payload)
	return out, nil
}

// AllSchemaNames returns the event-type names for every produced event schema
// embedded under schemas/.
func AllSchemaNames() ([]string, error) {
	entries, err := fs.ReadDir(schemasRaw, "schemas")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := eventTypeFromSchemaFile(e.Name())
		if domain.IsProducedEvent(name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// schemaFileNames maps every embedded schemas/*.json filename to its event type.
var schemaFileNames = map[string]string{
	"tenant_entitlements_changed.json": domain.EventTypeTenantEntitlementsChanged,
	"tenant_quota_add_on_changed.json": domain.EventTypeTenantQuotaAddOnChanged,
}

func eventTypeFromSchemaFile(filename string) string {
	if name, ok := schemaFileNames[filename]; ok {
		return name
	}
	return strings.TrimSuffix(filename, ".json")
}
