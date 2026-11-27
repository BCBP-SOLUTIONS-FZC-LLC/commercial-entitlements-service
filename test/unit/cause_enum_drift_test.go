package unit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestTenantEntitlementsChangedCause_NoDrift pins the cause enum of the
// produced TenantEntitlementsChanged event (F-4): the domain constants, the
// embedded schema extracted from api/asyncapi.yaml (what Glue and every
// subscriber see) and the LLD §7.1 field table must list the same values.
func TestTenantEntitlementsChangedCause_NoDrift(t *testing.T) {
	root := repoRoot(t)
	var want []string
	for _, c := range domain.EffectiveSetCauses {
		want = append(want, string(c))
	}

	raw, err := os.ReadFile(filepath.Join(root, "internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json"))
	require.NoError(t, err)
	var schema struct {
		Properties struct {
			Cause struct {
				Enum []string `json:"enum"`
			} `json:"cause"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	assert.Equal(t, want, schema.Properties.Cause.Enum, "embedded schema (api/asyncapi.yaml) vs domain.EffectiveSetCauses")

	lld, err := os.ReadFile(filepath.Join(root, "docs/lld/commercial-lld-entitlements-service.md"))
	require.NoError(t, err)
	row := regexp.MustCompile("(?m)^\\| `cause` \\| One of: (.*?) \\|").FindSubmatch(lld)
	require.NotNil(t, row, "LLD §7.1 `cause` row not found")
	var got []string
	for _, m := range regexp.MustCompile("`([a-z_]+)`").FindAllSubmatch(row[1], -1) {
		got = append(got, string(m[1]))
	}
	assert.Equal(t, want, got, "LLD §7.1 cause list vs domain.EffectiveSetCauses: %s", strings.TrimSpace(string(row[1])))
}
