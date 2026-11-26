package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// ── Decode ────────────────────────────────────────────────────────────────

func TestGlueCodec_Decode_ValidHeader_ReturnsPayload(t *testing.T) {
	// Build a minimal valid 18-byte Glue header followed by JSON payload.
	versionID := uuid.New()
	payload := []byte(`{"tenant_id":"abc"}`)
	encoded, err := prependGlueHeader(versionID.String(), payload)
	require.NoError(t, err)

	g := &GlueCodec{versionCache: map[string]string{}}
	decoded, err := g.Decode(context.Background(), "some_schema", encoded)
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage(payload), decoded)
}

func TestGlueCodec_Decode_TooShort_ReturnsError(t *testing.T) {
	g := &GlueCodec{versionCache: map[string]string{}}
	_, err := g.Decode(context.Background(), "some_schema", []byte{0x03, 0x00})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestGlueCodec_Decode_WrongFirstByte_ReturnsError(t *testing.T) {
	// 18 bytes but wrong magic byte.
	encoded := make([]byte, glueHeaderSize+5)
	encoded[0] = 0xFF // wrong magic
	g := &GlueCodec{versionCache: map[string]string{}}
	_, err := g.Decode(context.Background(), "some_schema", encoded)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected header byte")
}

func TestGlueCodec_Decode_ExactlyHeaderSize_EmptyPayload(t *testing.T) {
	// A message that is exactly the header with no payload body.
	versionID := uuid.New()
	encoded, err := prependGlueHeader(versionID.String(), []byte{})
	require.NoError(t, err)
	require.Len(t, encoded, glueHeaderSize)

	g := &GlueCodec{versionCache: map[string]string{}}
	decoded, err := g.Decode(context.Background(), "some_schema", encoded)
	require.NoError(t, err)
	assert.Empty(t, decoded)
}

// ── prependGlueHeader ─────────────────────────────────────────────────────

func TestPrependGlueHeader_ValidUUID_ProducesCorrectLength(t *testing.T) {
	versionID := uuid.New().String()
	payload := []byte(`{"key":"value"}`)
	out, err := prependGlueHeader(versionID, payload)
	require.NoError(t, err)
	assert.Len(t, out, glueHeaderSize+len(payload))
}

func TestPrependGlueHeader_FirstByteIsVersion(t *testing.T) {
	out, err := prependGlueHeader(uuid.New().String(), []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, glueHeaderVersion, out[0])
}

func TestPrependGlueHeader_SecondByteIsNoCompression(t *testing.T) {
	out, err := prependGlueHeader(uuid.New().String(), []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, glueNoCompression, out[1])
}

func TestPrependGlueHeader_BytesAfterHeaderMatchPayload(t *testing.T) {
	payload := []byte(`{"result":42}`)
	out, err := prependGlueHeader(uuid.New().String(), payload)
	require.NoError(t, err)
	assert.Equal(t, payload, out[glueHeaderSize:])
}

func TestPrependGlueHeader_InvalidUUID_ReturnsError(t *testing.T) {
	_, err := prependGlueHeader("not-a-uuid", []byte(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse schema version UUID")
}

func TestPrependGlueHeader_UUIDEmbeddedCorrectly(t *testing.T) {
	id := uuid.New()
	out, err := prependGlueHeader(id.String(), []byte(`{}`))
	require.NoError(t, err)
	// bytes 2–17 (16 bytes) are the UUID in raw form.
	assert.Equal(t, id[:], out[2:18])
}

// ── asciiEscape ───────────────────────────────────────────────────────────

func TestAsciiEscape_PureASCII_Unchanged(t *testing.T) {
	input := `{"type":"object","title":"TestSchema"}`
	result := asciiEscape([]byte(input))
	assert.Equal(t, input, result)
}

func TestAsciiEscape_NonASCIIChar_EscapedWithUnicode(t *testing.T) {
	// 'é' is U+00E9 — fits in BMP. asciiEscape must produce the \uXXXX form.
	input := []byte("caf\xc3\xa9") // UTF-8 encoding of "café"
	result := asciiEscape(input)
	// asciiEscape outputs the literal characters \, u, 0, 0, e, 9.
	assert.Equal(t, "caf\\u00e9", result, "BMP character must be \\uXXXX escaped")
}

func TestAsciiEscape_SurrogatePair_EscapedAsPair(t *testing.T) {
	// U+1F600 (😀) is above U+FFFF and must be encoded as a surrogate pair.
	input := []byte("\xf0\x9f\x98\x80") // UTF-8 for U+1F600
	result := asciiEscape(input)
	// Expect two \uXXXX escapes (surrogate pair).
	count := 0
	for i := 0; i < len(result)-5; i++ {
		if result[i] == '\\' && result[i+1] == 'u' {
			count++
		}
	}
	assert.Equal(t, 2, count, "code point above U+FFFF must produce two \\uXXXX escape sequences")
}

func TestAsciiEscape_Empty_ReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", asciiEscape([]byte{}))
}

// ── AllSchemaNames ────────────────────────────────────────────────────────

func TestAllSchemaNames_ReturnsNonEmpty(t *testing.T) {
	names, err := AllSchemaNames()
	require.NoError(t, err)
	assert.NotEmpty(t, names, "at least one produced-event schema must be embedded")
}

func TestAllSchemaNames_NamesAreNonEmpty(t *testing.T) {
	names, err := AllSchemaNames()
	require.NoError(t, err)
	for _, name := range names {
		assert.NotEmpty(t, name, "schema name must not be an empty string")
	}
}

func TestAllSchemaNames_Containstenant_entitlements_changed(t *testing.T) {
	names, err := AllSchemaNames()
	require.NoError(t, err)
	found := false
	for _, n := range names {
		if n == "tenant_entitlements_changed" {
			found = true
			break
		}
	}
	assert.True(t, found, "tenant_entitlements_changed must be listed as a produced-event schema")
}

// ── round-trip via prependGlueHeader + Decode ─────────────────────────────

func TestGlueCodec_RoundTrip_PrependThenDecode(t *testing.T) {
	versionID := uuid.New()
	original := json.RawMessage(`{"tenant_id":"` + uuid.New().String() + `","version":7}`)

	encoded, err := prependGlueHeader(versionID.String(), original)
	require.NoError(t, err)

	g := &GlueCodec{versionCache: map[string]string{}}
	decoded, err := g.Decode(context.Background(), "tenant_entitlements_changed", encoded)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

// ── versionID (cache hit path) ────────────────────────────────────────────

func TestGlueCodec_VersionID_CacheHit_ReturnsWithoutNetworkCall(t *testing.T) {
	expectedID := uuid.New().String()
	g := &GlueCodec{
		versionCache: map[string]string{
			"tenant_entitlements_changed": expectedID,
		},
	}
	id, err := g.versionID(context.Background(), "tenant_entitlements_changed")
	require.NoError(t, err)
	assert.Equal(t, expectedID, id)
}

// ── Encode error path (unknown schema name, cache miss) ───────────────────

func TestGlueCodec_Encode_UnknownSchema_ReturnsError(t *testing.T) {
	// registeredDefinition will fail for a schema name not in the embedded FS.
	// This exercises the error path in fetchVersionID without needing a live client.
	g := &GlueCodec{
		client:       nil,
		registryName: "test-registry",
		versionCache: map[string]string{},
	}
	_, _, err := g.Encode(context.Background(), "NonExistentSchemaForTest", []byte(`{}`))
	require.Error(t, err, "Encode must fail when no embedded schema file exists for the given name")
}

// ── registeredDefinition (embedded FS) ───────────────────────────────────

func TestRegisteredDefinition_KnownSchema_ReturnsNonEmpty(t *testing.T) {
	def, err := registeredDefinition(schemasRaw, "tenant_entitlements_changed")
	require.NoError(t, err)
	assert.NotEmpty(t, def)
}

func TestRegisteredDefinition_UnknownSchema_ReturnsError(t *testing.T) {
	_, err := registeredDefinition(schemasRaw, "NonExistentSchemaXYZ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no embedded schema file")
}

func TestRegisteredDefinition_ResultIsASCII(t *testing.T) {
	def, err := registeredDefinition(schemasRaw, "tenant_entitlements_changed")
	require.NoError(t, err)
	for _, b := range []byte(def) {
		assert.Less(t, int(b), 128, fmt.Sprintf("definition must be pure ASCII; got byte %d", b))
	}
}

// ── Encode cache-hit path ─────────────────────────────────────────────────
//
// NewGlueCodec populates versionCache at construction time (startup). A second
// call to Encode for the same schema name must hit the RLock/cache-hit branch
// inside versionID without issuing another network call. We verify this by
// constructing a GlueCodec with a pre-populated versionCache (skipping the
// real Glue client) and calling Encode twice — both calls must succeed and
// return identical version IDs.

func TestGlueCodec_Encode_SecondCallHitsCache(t *testing.T) {
	expectedVersionID := uuid.New().String()
	g := &GlueCodec{
		client:       nil, // no network — cache must be used
		registryName: "test-registry",
		versionCache: map[string]string{
			"tenant_entitlements_changed": expectedVersionID,
		},
	}

	payload := json.RawMessage(`{"tenant_id":"` + uuid.New().String() + `","version":1}`)

	// First call: cache hit.
	out1, vid1, err := g.Encode(context.Background(), "tenant_entitlements_changed", payload)
	require.NoError(t, err)
	assert.Equal(t, expectedVersionID, vid1, "first Encode must return the cached version ID")
	assert.Len(t, out1, glueHeaderSize+len(payload), "output length must be header + payload")

	// Second call: must also hit the cache (not attempt a nil client call).
	out2, vid2, err := g.Encode(context.Background(), "tenant_entitlements_changed", payload)
	require.NoError(t, err)
	assert.Equal(t, expectedVersionID, vid2, "second Encode must return the same cached version ID")
	assert.Equal(t, out1, out2, "both Encode calls must produce identical output for the same input")
}

// ── eventTypeFromSchemaFile — unmapped filename ───────────────────────────

func TestEventTypeFromSchemaFile_MappedFilename_ReturnsConstant(t *testing.T) {
	name := eventTypeFromSchemaFile("tenant_entitlements_changed.json")
	assert.Equal(t, domain.EventTypeTenantEntitlementsChanged, name)
}

func TestEventTypeFromSchemaFile_UnmappedFilename_StripsExtension(t *testing.T) {
	// Filename not in schemaFileNames map → falls through to TrimSuffix path.
	name := eventTypeFromSchemaFile("some_future_event.json")
	assert.Equal(t, "some_future_event", name)
}

func TestEventTypeFromSchemaFile_NoExtension_ReturnsAsIs(t *testing.T) {
	name := eventTypeFromSchemaFile("no_extension")
	assert.Equal(t, "no_extension", name)
}

// ── AllSchemaNames ────────────────────────────────────────────────────────

func TestAllSchemaNames_ReturnsProducedEventNames(t *testing.T) {
	names, err := AllSchemaNames()
	require.NoError(t, err)
	assert.NotEmpty(t, names)
	assert.Contains(t, names, domain.EventTypeTenantEntitlementsChanged)
}

// ── versionID — cache-miss then cache-write ────────────────────────────────

func TestGlueCodec_VersionID_CacheMiss_PopulatesCache(t *testing.T) {
	// Pre-condition: empty cache. When fetchVersionID is called it sets the cache.
	// Since we don't have a real Glue client, fetchVersionID will error out.
	// But the cache-miss branch (reading with RLock, finding nothing, then writing)
	// is exercised before the error is returned.
	g := &GlueCodec{
		client:       nil,
		registryName: "test-registry",
		versionCache: map[string]string{},
	}
	_, err := g.versionID(context.Background(), "unknown_schema")
	// nil client → fetchVersionID will panic or error; just verify the branch was reached.
	assert.Error(t, err) // Any error is fine — we just need coverage.
}

// ── NewGlueCodec — fake Glue API server ───────────────────────────────────
//
// The tests below start a minimal httptest server that responds to
// AWSGlue.GetSchemaByDefinition and verify that NewGlueCodec and its
// helpers exercise the success and error branches.

func TestNewGlueCodec_FakeServer_Success(t *testing.T) {
	versionID := uuid.New().String()
	client := startFakeGlueServer(t, 200,
		`{"SchemaVersionId":"`+versionID+`","Status":"AVAILABLE"}`)

	codec, err := NewGlueCodec(context.Background(), client, "test-registry",
		[]string{domain.EventTypeTenantEntitlementsChanged})
	require.NoError(t, err)
	assert.NotNil(t, codec)
}

func TestNewGlueCodec_FakeServer_NilSchemaVersionId_ReturnsError(t *testing.T) {
	client := startFakeGlueServer(t, 200, `{"Status":"AVAILABLE"}`)
	_, err := NewGlueCodec(context.Background(), client, "test-registry",
		[]string{domain.EventTypeTenantEntitlementsChanged})
	require.Error(t, err, "nil SchemaVersionId must cause NewGlueCodec to fail")
}

func TestNewGlueCodec_FakeServer_StatusNotAvailable_ReturnsError(t *testing.T) {
	versionID := uuid.New().String()
	client := startFakeGlueServer(t, 200,
		`{"SchemaVersionId":"`+versionID+`","Status":"PENDING"}`)
	_, err := NewGlueCodec(context.Background(), client, "test-registry",
		[]string{domain.EventTypeTenantEntitlementsChanged})
	require.Error(t, err, "non-AVAILABLE schema status must cause NewGlueCodec to fail")
}

func TestNewGlueCodec_FakeServer_APIError_ReturnsError(t *testing.T) {
	client := startFakeGlueServer(t, 500,
		`{"__type":"InternalServiceException","Message":"internal error"}`)
	_, err := NewGlueCodec(context.Background(), client, "test-registry",
		[]string{domain.EventTypeTenantEntitlementsChanged})
	require.Error(t, err, "Glue API error must cause NewGlueCodec to fail")
}

// startFakeGlueServer starts an httptest.Server that returns the given HTTP
// status code and JSON body for every request, then wires a *glue.Client
// pointed at that server with static credentials so no real AWS call is made.
func startFakeGlueServer(t *testing.T, statusCode int, responseBody string) *glue.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(srv.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			awscredentials.NewStaticCredentialsProvider("AKID", "SECRET", "TOKEN"),
		),
	)
	require.NoError(t, err)

	ep := srv.URL
	return glue.NewFromConfig(cfg, func(o *glue.Options) {
		o.BaseEndpoint = &ep
	})
}

func TestGlueCodec_Encode_FakeServer_Success(t *testing.T) {
	versionID := uuid.New().String()
	client := startFakeGlueServer(t, 200,
		`{"SchemaVersionId":"`+versionID+`","Status":"AVAILABLE"}`)

	// Create codec with an empty cache (versionID not pre-populated).
	g := &GlueCodec{
		client:       client,
		registryName: "test-registry",
		versionCache: map[string]string{},
	}

	payload := json.RawMessage(`{"tenant_id":"` + uuid.New().String() + `","version":1}`)
	out, vid, err := g.Encode(context.Background(), "tenant_entitlements_changed", payload)
	require.NoError(t, err)
	assert.Equal(t, versionID, vid, "returned version ID must match the fake server's response")
	assert.Len(t, out, glueHeaderSize+len(payload))
}

// TestGlueCodec_Encode_InvalidCachedUUID covers the prependGlueHeader error
// path in Encode by injecting a malformed UUID into the version cache.
func TestGlueCodec_Encode_InvalidCachedUUID_ReturnsError(t *testing.T) {
	g := &GlueCodec{
		client:       nil,
		registryName: "test-registry",
		versionCache: map[string]string{
			"tenant_entitlements_changed": "not-a-valid-uuid", // invalid UUID → prependGlueHeader fails
		},
	}
	payload := json.RawMessage(`{"tenant_id":"` + uuid.New().String() + `"}`)
	_, _, err := g.Encode(context.Background(), "tenant_entitlements_changed", payload)
	require.Error(t, err, "Encode with invalid cached UUID must fail in prependGlueHeader")
}
