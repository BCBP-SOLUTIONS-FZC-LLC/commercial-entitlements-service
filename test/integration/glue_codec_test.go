//go:build integration

// Glue schema-version resolution against a real (floci) Glue Schema Registry.
//
// Verifies GlueCodec stamps events with the version matching THIS binary's
// embedded schema (GetSchemaByDefinition) — not the registry's latest.
// This is the property that an older LocalStack-style "fetch latest version at
// startup + refresh every 5 min" approach got wrong during deploy/registration
// races and rollbacks. Pattern ported directly from iam-org-membership Phase 12.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventbusadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// newFlociGlue returns a *glue.Client pointed at the shared floci container.
func newFlociGlue(t *testing.T) *glue.Client {
	t.Helper()
	endpoint := startFloci(t)
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(flociRegion),
		awsconfig.WithCredentialsProvider(
			awscreds.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)
	return glue.NewFromConfig(cfg, func(o *glue.Options) { o.BaseEndpoint = &endpoint })
}

// registeredForm returns the embedded schema file in the compact, ASCII-safe
// form that schema-gov uploads to Glue (json.Compact on the raw file).
// Reads directly from disk — same approach as iam-org-membership.
func registeredForm(t *testing.T, schemaFile string) string {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "adapter", "outbound", "eventbus", "schemas", schemaFile)
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "schema file must exist at %s", path)
	var buf bytes.Buffer
	require.NoError(t, json.Compact(&buf, raw))
	return buf.String()
}

// createGlueRegistry creates a uniquely-named registry in floci and returns its name.
func createGlueRegistry(t *testing.T, cli *glue.Client) string {
	t.Helper()
	name := "ce-it-" + uuid.NewString()[:8]
	_, err := cli.CreateRegistry(context.Background(), &glue.CreateRegistryInput{
		RegistryName: &name,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = cli.DeleteRegistry(context.Background(), &glue.DeleteRegistryInput{
			RegistryId: &gluetypes.RegistryId{RegistryName: &name},
		})
	})
	return name
}

// createGlueSchema registers a schema definition and returns its version UUID.
func createGlueSchema(t *testing.T, cli *glue.Client, registry, schemaName, definition string) string {
	t.Helper()
	out, err := cli.CreateSchema(context.Background(), &glue.CreateSchemaInput{
		RegistryId:       &gluetypes.RegistryId{RegistryName: &registry},
		SchemaName:       &schemaName,
		DataFormat:       gluetypes.DataFormatJson,
		Compatibility:    gluetypes.CompatibilityBackward,
		SchemaDefinition: &definition,
	})
	require.NoError(t, err)
	return aws.ToString(out.SchemaVersionId)
}

// stampedVersionID calls codec.Encode and returns the Glue version UUID it stamped.
func stampedVersionID(t *testing.T, codec *eventbusadapter.GlueCodec, schemaName string) string {
	t.Helper()
	_, vid, err := codec.Encode(context.Background(), schemaName, json.RawMessage(`{}`))
	require.NoError(t, err)
	return vid
}

// ── P6-GLUE-001 ───────────────────────────────────────────────────────────────

// TestGlueCodec_ResolvesOwnVersion_NotLatest verifies the codec stamps the
// schema version whose definition MATCHES the binary's embedded schema — not
// whatever happens to be the registry's latest at startup.
//
// Failure mode covered: v2 is registered after this binary was built. The binary
// must still stamp v1 (the version it ships) rather than v2.
func TestGlueCodec_ResolvesOwnVersion_NotLatest(t *testing.T) {
	cli := newFlociGlue(t)
	registry := createGlueRegistry(t, cli)
	schemaName := domain.EventTypeTenantEntitlementsChanged

	// v1 — matches the binary's embedded schema definition exactly.
	v1Def := registeredForm(t, "tenant_entitlements_changed.json")
	v1 := createGlueSchema(t, cli, registry, schemaName, v1Def)

	// v2 — a BACKWARD-compatible annotation-only change so Glue accepts it.
	var v1Doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(v1Def), &v1Doc))
	v1Doc["description"] = "v2 annotation — does not change schema shape"
	v2Def, err := json.Marshal(v1Doc)
	require.NoError(t, err)

	v2Out, err := cli.RegisterSchemaVersion(context.Background(), &glue.RegisterSchemaVersionInput{
		SchemaId:         &gluetypes.SchemaId{RegistryName: &registry, SchemaName: &schemaName},
		SchemaDefinition: aws.String(string(v2Def)),
	})
	require.NoError(t, err)
	v2 := aws.ToString(v2Out.SchemaVersionId)
	require.NotEqual(t, v1, v2, "precondition: two distinct versions must have been registered")

	// Confirm v2 is now the registry latest.
	latest, err := cli.GetSchemaVersion(context.Background(), &glue.GetSchemaVersionInput{
		SchemaId:            &gluetypes.SchemaId{RegistryName: &registry, SchemaName: &schemaName},
		SchemaVersionNumber: &gluetypes.SchemaVersionNumber{LatestVersion: true},
	})
	require.NoError(t, err)
	require.Equal(t, v2, aws.ToString(latest.SchemaVersionId), "precondition: v2 must be the registry latest")

	// Codec must resolve v1 by content (GetSchemaByDefinition), not v2 by position.
	codec, err := eventbusadapter.NewGlueCodec(context.Background(), cli, registry, []string{schemaName})
	require.NoError(t, err)

	assert.Equal(t, v1, stampedVersionID(t, codec, schemaName),
		"codec must stamp the version matching the embedded (v1) schema, not the registry latest (v2)")
}

// ── P6-GLUE-002 ───────────────────────────────────────────────────────────────

// TestGlueCodec_ResolvesAllProducedSchemas verifies that every schema returned
// by AllSchemaNames (every event this service produces) resolves to the version
// that matches its own embedded definition.
func TestGlueCodec_ResolvesAllProducedSchemas(t *testing.T) {
	cli := newFlociGlue(t)
	registry := createGlueRegistry(t, cli)

	names, err := eventbusadapter.AllSchemaNames()
	require.NoError(t, err)
	require.NotEmpty(t, names, "AllSchemaNames must return at least one produced-event schema")

	// Register each schema and record the version UUID we expect the codec to stamp.
	want := make(map[string]string, len(names))
	for _, name := range names {
		want[name] = createGlueSchema(t, cli, registry, name, registeredForm(t, name+".json"))
	}

	codec, err := eventbusadapter.NewGlueCodec(context.Background(), cli, registry, names)
	require.NoError(t, err)

	for name, expectedVID := range want {
		assert.Equal(t, expectedVID, stampedVersionID(t, codec, name),
			"schema %q: codec must stamp its own registered version", name)
	}
}

// ── P6-GLUE-003 ───────────────────────────────────────────────────────────────

// TestGlueCodec_UnregisteredDefinition_FailsStartup verifies that NewGlueCodec
// fails fast when the definition in the registry doesn't match the binary's
// embedded schema (deploy outran registration), rather than silently stamping
// a wrong version or panicking at event-publish time.
func TestGlueCodec_UnregisteredDefinition_FailsStartup(t *testing.T) {
	cli := newFlociGlue(t)
	registry := createGlueRegistry(t, cli)
	schemaName := domain.EventTypeTenantEntitlementsChanged

	// Register a stub that intentionally doesn't match the binary's embedded schema.
	createGlueSchema(t, cli, registry, schemaName,
		`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":true,"description":"wrong version — not what the binary ships"}`)

	_, err := eventbusadapter.NewGlueCodec(context.Background(), cli, registry, []string{schemaName})
	require.Error(t, err,
		"NewGlueCodec must fail when the registered definition does not match the embedded schema")
	assert.Contains(t, err.Error(), schemaName,
		"error must name the failing schema so operators know what to register")

	// Schema absent from registry entirely must also fail startup.
	_, err = eventbusadapter.NewGlueCodec(context.Background(), cli, registry,
		[]string{"NonExistentSchemaForTest"})
	require.Error(t, err, "NewGlueCodec must fail when the schema is entirely absent from the registry")
}
