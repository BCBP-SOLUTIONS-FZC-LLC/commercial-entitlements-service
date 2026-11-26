package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	apispec "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/api"
)

// ── readAsyncSpec ────────────────────────────────────────────────────────

func TestReadAsyncSpec_EmbeddedSpec_ParsesOK(t *testing.T) {
	spec, err := readAsyncSpec(apispec.AsyncAPISpec)
	require.NoError(t, err)
	assert.NotEmpty(t, spec.Info.Title)
	assert.NotEmpty(t, spec.AsyncAPI)
	assert.NotEmpty(t, spec.Comps.Messages)
	assert.NotEmpty(t, spec.Comps.Schemas)
}

func TestReadAsyncSpec_InvalidYAML_ReturnsError(t *testing.T) {
	// A mapping with a duplicate key at root level causes yaml.v3 to error.
	_, err := readAsyncSpec([]byte("key: [unclosed"))
	require.Error(t, err)
}

// ── renderPage ────────────────────────────────────────────────────────────

func TestRenderPage_ContainsExpectedSections(t *testing.T) {
	spec, err := readAsyncSpec(apispec.AsyncAPISpec)
	require.NoError(t, err)

	var buf bytes.Buffer
	renderPage(&buf, spec, "dev")
	html := buf.String()

	assert.Contains(t, html, "Published Messages")
	assert.Contains(t, html, "Consumed Messages")
	assert.Contains(t, html, "Schemas")
	assert.Contains(t, html, "Servers")
}

func TestRenderPage_ProdEnv_RedBadge(t *testing.T) {
	spec, err := readAsyncSpec(apispec.AsyncAPISpec)
	require.NoError(t, err)
	var buf bytes.Buffer
	renderPage(&buf, spec, "prod")
	assert.Contains(t, buf.String(), "PROD")
}

// ── walkAsyncYAML ─────────────────────────────────────────────────────────

func TestWalkAsyncYAML_NilNode_ReturnsEmpty(t *testing.T) {
	assert.Empty(t, walkAsyncYAML(nil, "key"))
}

func TestWalkAsyncYAML_NoKeys_ReturnsEmpty(t *testing.T) {
	var n yaml.Node
	assert.Empty(t, walkAsyncYAML(&n))
}

func TestWalkAsyncYAML_MissingKey_ReturnsEmpty(t *testing.T) {
	raw := `{"sns": {"messageAttributes": {"EventType": {"value": "MyEvent"}}}}`
	var n yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &n))
	assert.Empty(t, walkAsyncYAML(&n, "sns", "messageAttributes", "Missing", "value"))
}

func TestWalkAsyncYAML_Found_ReturnsValue(t *testing.T) {
	raw := `sns:
  messageAttributes:
    EventType:
      value: "TenantEntitlementsChanged"`
	var n yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &n))
	got := walkAsyncYAML(&n, "sns", "messageAttributes", "EventType", "value")
	assert.Equal(t, "TenantEntitlementsChanged", got)
}

// ── snsEventType ─────────────────────────────────────────────────────────

func TestSnsEventType_NilNode_ReturnsEmpty(t *testing.T) {
	assert.Empty(t, snsEventType(nil))
}

func TestSnsEventType_WithEventType_ReturnsValue(t *testing.T) {
	raw := `sns:
  messageAttributes:
    EventType:
      value: "TenantEntitlementsChanged"`
	var n yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &n))
	assert.Equal(t, "TenantEntitlementsChanged", snsEventType(&n))
}

func TestSnsEventType_SnakeCaseFallback_ReturnsValue(t *testing.T) {
	raw := `sns:
  messageAttributes:
    event_type:
      value: "TenantPlanChanged"`
	var n yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &n))
	assert.Equal(t, "TenantPlanChanged", snsEventType(&n))
}

// ── asyncTypeHTML / asyncPropType ─────────────────────────────────────────

func TestAsyncTypeHTML_SimplePrimitive(t *testing.T) {
	p := &asyncProp{Type: "string", Format: "uuid"}
	html := asyncTypeHTML(p)
	assert.Contains(t, html, "string(uuid)")
	assert.Contains(t, html, "prop-type")
}

func TestAsyncTypeHTML_RefProp_LinksToSchema(t *testing.T) {
	p := &asyncProp{Ref: "#/components/schemas/TenantEntitlementsChangedPayload"}
	html := asyncTypeHTML(p)
	assert.Contains(t, html, `href="#schema-TenantEntitlementsChangedPayload"`)
	assert.Contains(t, html, "TenantEntitlementsChangedPayload")
}

func TestAsyncTypeHTML_ArrayOfRef_LinksToItemSchema(t *testing.T) {
	p := &asyncProp{
		Type:  "array",
		Items: &asyncProp{Ref: "#/components/schemas/InboundTenantStateChangedPayload"},
	}
	html := asyncTypeHTML(p)
	assert.Contains(t, html, "array")
	assert.Contains(t, html, "InboundTenantStateChangedPayload")
}

func TestAsyncTypeHTML_ArrayOfPrimitive(t *testing.T) {
	p := &asyncProp{Type: "array", Items: &asyncProp{Type: "string"}}
	// asyncTypeHTML HTML-escapes < and > so array<string> renders as array&lt;string&gt;
	assert.Equal(t, "array<string>", asyncPropType(p))
}

// ── resolveAsyncSchema ────────────────────────────────────────────────────

func TestResolveAsyncSchema_NoAllOf_ReturnsSelf(t *testing.T) {
	sc := &asyncSchema{
		Type:       "object",
		Properties: map[string]asyncProp{"id": {Type: "string"}},
	}
	got := resolveAsyncSchema(sc, &asyncComponents{})
	assert.Equal(t, sc, got)
}

func TestResolveAsyncSchema_WithRef_ResolvesFromComponents(t *testing.T) {
	inner := asyncSchema{
		Type:       "object",
		Properties: map[string]asyncProp{"name": {Type: "string"}},
	}
	comps := &asyncComponents{
		Schemas: map[string]asyncSchema{"Inner": inner},
	}
	sc := &asyncSchema{Ref: "#/components/schemas/Inner"}
	got := resolveAsyncSchema(sc, comps)
	assert.Equal(t, "string", got.Properties["name"].Type)
}

func TestResolveAsyncSchema_AllOf_MergesProperties(t *testing.T) {
	comps := &asyncComponents{Schemas: map[string]asyncSchema{}}
	sc := &asyncSchema{
		AllOf: []asyncSchema{
			{
				Properties:    map[string]asyncProp{"a": {Type: "string"}},
				PropertyOrder: []string{"a"},
			},
			{
				Properties:    map[string]asyncProp{"b": {Type: "integer"}},
				PropertyOrder: []string{"b"},
			},
		},
	}
	got := resolveAsyncSchema(sc, comps)
	assert.Equal(t, "string", got.Properties["a"].Type)
	assert.Equal(t, "integer", got.Properties["b"].Type)
	assert.Equal(t, []string{"a", "b"}, got.PropertyOrder)
}

// ── SwaggerThemeHandler / SwaggerInitializerHandler ───────────────────────

func TestSwaggerThemeHandler_ReturnsCSSWithGradient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/swagger/swagger-theme.css", nil)

	SwaggerThemeHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/css")
	assert.Contains(t, w.Body.String(), "#8426b0")
}

func TestSwaggerInitializerHandler_ReturnsJS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/swagger/swagger-initializer.js", nil)

	SwaggerInitializerHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "javascript")
	assert.Contains(t, w.Body.String(), "SwaggerUIBundle")
}

// ── Router — swagger theme + initializer via full router ──────────────────

func TestRouter_SwaggerTheme_DocsEnabled_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: true})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/swagger/swagger-theme.css", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "#8426b0")
}

func TestRouter_SwaggerInitializer_DocsEnabled_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: true})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/swagger/swagger-initializer.js", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "SwaggerUIBundle")
}

func TestRouter_SwaggerUI_DocsEnabled_200(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: true})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_SwaggerUI_DocsDisabled_404(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: false})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRouter_SwaggerUI_WithAuthToken_Unauthorized(t *testing.T) {
	r := buildTestRouter(DocsConfig{Enabled: true, AuthToken: "secret"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	r.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── consumedMessageNames ──────────────────────────────────────────────────

func TestConsumedMessageNames_EmptyOps_ReturnsEmpty(t *testing.T) {
	var n yaml.Node
	result := consumedMessageNames(&n)
	assert.Empty(t, result)
}

func TestConsumedMessageNames_ReceiveAction_Marked(t *testing.T) {
	raw := `
publishTenantEntitlementsChanged:
  action: send
  messages:
    - $ref: "#/components/messages/TenantEntitlementsChanged"
consumeTenantStateChanged:
  action: receive
  messages:
    - $ref: "#/components/messages/InboundTenantStateChanged"
`
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &doc))
	// yaml.Unmarshal wraps the content in a DocumentNode; pass the inner MappingNode.
	require.Equal(t, yaml.DocumentNode, doc.Kind)
	result := consumedMessageNames(doc.Content[0])
	assert.True(t, result["InboundTenantStateChanged"])
	assert.False(t, result["TenantEntitlementsChanged"])
}

// ── splitMessagesByDirection ──────────────────────────────────────────────

func TestSplitMessagesByDirection_PartitionsCorrectly(t *testing.T) {
	messages := map[string]asyncMessage{
		"Pub1": {Title: "Published 1"},
		"Con1": {Title: "Consumed 1"},
		"Pub2": {Title: "Published 2"},
	}
	consumed := map[string]bool{"Con1": true}
	pub, con := splitMessagesByDirection(messages, consumed)
	assert.Contains(t, pub, "Pub1")
	assert.Contains(t, pub, "Pub2")
	assert.Contains(t, con, "Con1")
	assert.NotContains(t, con, "Pub1")
}

// ── renderPropsTable edge cases ───────────────────────────────────────────

func TestRenderPropsTable_NilSchema_WritesNoProperties(t *testing.T) {
	var buf bytes.Buffer
	renderPropsTable(&buf, nil, "")
	assert.Contains(t, buf.String(), "No properties")
}

func TestRenderPropsTable_WithEnumAndExample_RendersAll(t *testing.T) {
	sc := &asyncSchema{
		Properties: map[string]asyncProp{
			"cause": {
				Type:    "string",
				Enum:    []string{"plan_change", "override_change"},
				Example: "plan_change",
				Desc:    "What triggered this recomputation.",
			},
		},
		Required:      []string{"cause"},
		PropertyOrder: []string{"cause"},
	}
	var buf bytes.Buffer
	renderPropsTable(&buf, sc, "TestSchema")
	html := buf.String()
	assert.Contains(t, html, "plan_change")
	assert.Contains(t, html, "required")
	assert.Contains(t, html, "What triggered")
}
