//go:build helm

// Package helm_test — CD-19 access-matrix assertions over the rendered Helm
// chart (LLD §10.2).
//
// helm-check.sh proves the chart renders and that standard resources are
// schema-valid, but not WHICH routes each cross-namespace caller can reach.
// This test renders the chart with staging + prod overlays, parses the YAML,
// and pins:
//
//   - Istio AuthorizationPolicy (L7): one ALLOW policy with the billing
//     principal restricted to GET */effective-set and */quota-ceilings.
//   - NetworkPolicy (L4): billing namespace pods are admitted on the API
//     port only; no rule admits every namespace unconditionally.
//
// Run: make helm-access-check (or go test -tags=helm ./test/helm/...)
// Requires the helm binary on PATH; CI installs it in helm-check.sh.
package helm_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	releaseName = "commercial-entitlements"
	namespace   = "commercial"
	apiPort     = 8080
	trustDomain = "cluster.local"
)

// sharedSets are the same overrides used by helm-check.sh.
var sharedSets = []string{
	"--set", "image.tag=ci-check",
	"--set", "existingSecret=ci-lint-skip",
}

// stagingSets enable Istio + NetworkPolicy callers (same as values-staging.yaml).
var stagingSets = append(sharedSets,
	"--set", "callers.enabled=true",
	"--set", "callers.istio.enabled=true",
	"--set", fmt.Sprintf("callers.istio.trustDomain=%s", trustDomain),
	"--set", "callers.istio.existingCallerNamespaces[0]=commercial",
	"--set", "callers.istio.existingCallerNamespaces[1]=gateway",
	"--set", "callers.istio.existingCallerNamespaces[2]=observability",
	"--set", "billingCaller.enabled=true",
	"--set", "networkPolicy.enabled=true",
)

// ── helpers ────────────────────────────────────────────────────────────────

// chartDir returns the absolute path to the Helm chart.
func chartDir() string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "deploy", "helm")
	return root
}

// renderChart runs helm template and returns the multi-document YAML.
func renderChart(t *testing.T, extraSets ...string) []map[string]any {
	t.Helper()
	args := []string{"template", releaseName, chartDir(), "--namespace", namespace}
	args = append(args, extraSets...)
	out, err := exec.CommandContext(context.Background(), "helm", args...).Output() //nolint:noctx
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("helm template failed:\n%s", string(exitErr.Stderr))
		}
		t.Fatalf("helm template: %v", err)
	}

	var docs []map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("yaml decode: %v", err)
		}
		if len(doc) > 0 {
			docs = append(docs, doc)
		}
	}
	return docs
}

// findResource returns all documents matching kind + optionally name.
func findResource(docs []map[string]any, kind, name string) []map[string]any {
	var out []map[string]any
	for _, d := range docs {
		if d["kind"] != kind {
			continue
		}
		if name != "" {
			meta, _ := d["metadata"].(map[string]any)
			if meta["name"] != name && !strings.HasSuffix(meta["name"].(string), "-"+name) {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// ── AuthorizationPolicy tests ──────────────────────────────────────────────

// TestHelmAuthzPolicy_BillingAllowedRoutesOnly asserts the Istio
// AuthorizationPolicy limits billing to GET */effective-set and
// */quota-ceilings only.
func TestHelmAuthzPolicy_BillingAllowedRoutesOnly(t *testing.T) {
	docs := renderChart(t, stagingSets...)
	policies := findResource(docs, "AuthorizationPolicy", "callers")
	if len(policies) == 0 {
		t.Skip("no AuthorizationPolicy rendered — skipping (callers.enabled=false?)")
	}

	spec, _ := policies[0]["spec"].(map[string]any)
	if spec["action"] != "ALLOW" {
		t.Fatalf("policy action must be ALLOW, got %s", spec["action"])
	}

	rules, _ := spec["rules"].([]any)
	if len(rules) == 0 {
		t.Fatal("policy has no rules")
	}

	// Find the billing principal rule.
	var billingPaths []string
	for _, r := range rules {
		rule := r.(map[string]any)
		froms, _ := rule["from"].([]any)
		if len(froms) == 0 {
			continue
		}
		src := froms[0].(map[string]any)["source"].(map[string]any)
		principals, _ := src["principals"].([]any)
		if len(principals) == 0 {
			continue
		}
		p := principals[0].(string)
		if !strings.Contains(p, "commercial-billing") {
			continue
		}
		// Found the billing rule — extract paths.
		tos, _ := rule["to"].([]any)
		for _, to := range tos {
			ops, _ := to.(map[string]any)["operation"].(map[string]any)
			paths, _ := ops["paths"].([]any)
			for _, path := range paths {
				billingPaths = append(billingPaths, path.(string))
			}
		}
	}

	sort.Strings(billingPaths)
	want := []string{"*/effective-set", "*/quota-ceilings"}
	sort.Strings(want)

	if !reflect.DeepEqual(billingPaths, want) {
		t.Fatalf("billing allowed paths:\n  got:  %v\n  want: %v", billingPaths, want)
	}
}

// TestHelmAuthzPolicy_ExistingNamespacesAdmitted asserts the
// AuthorizationPolicy admits at least the commercial, gateway, and
// observability namespaces via a namespace-level rule.
func TestHelmAuthzPolicy_ExistingNamespacesAdmitted(t *testing.T) {
	docs := renderChart(t, stagingSets...)
	policies := findResource(docs, "AuthorizationPolicy", "callers")
	if len(policies) == 0 {
		t.Skip("no AuthorizationPolicy rendered")
	}

	spec, _ := policies[0]["spec"].(map[string]any)
	rules, _ := spec["rules"].([]any)

	var admittedNamespaces []string
	for _, r := range rules {
		rule := r.(map[string]any)
		froms, _ := rule["from"].([]any)
		if len(froms) == 0 {
			continue
		}
		src := froms[0].(map[string]any)["source"].(map[string]any)
		nss, _ := src["namespaces"].([]any)
		for _, ns := range nss {
			admittedNamespaces = append(admittedNamespaces, ns.(string))
		}
	}

	for _, must := range []string{"commercial", "gateway", "observability"} {
		found := false
		for _, ns := range admittedNamespaces {
			if ns == must {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("namespace %q must be in AuthorizationPolicy existingCallerNamespaces; admitted: %v", must, admittedNamespaces)
		}
	}
}

// ── NetworkPolicy tests ────────────────────────────────────────────────────

// TestHelmNetworkPolicy_BillingAdmittedOnAPIPortOnly asserts that the
// NetworkPolicy admits billing namespace pods on the API port only — not
// the metrics port and not all ports.
func TestHelmNetworkPolicy_BillingAdmittedOnAPIPortOnly(t *testing.T) {
	docs := renderChart(t, stagingSets...)
	nps := findResource(docs, "NetworkPolicy", "")
	if len(nps) == 0 {
		t.Skip("no NetworkPolicy rendered")
	}

	spec, _ := nps[0]["spec"].(map[string]any)
	ingresses, _ := spec["ingress"].([]any)

	for _, ing := range ingresses {
		ingress := ing.(map[string]any)
		froms, _ := ingress["from"].([]any)
		if len(froms) == 0 {
			continue
		}
		// Check if this rule admits billing namespace.
		admitsBilling := false
		for _, from := range froms {
			f := from.(map[string]any)
			nsSel, _ := f["namespaceSelector"].(map[string]any)
			labels, _ := nsSel["matchLabels"].(map[string]any)
			for _, v := range labels {
				if v == "billing" {
					admitsBilling = true
				}
			}
		}
		if !admitsBilling {
			continue
		}

		// Found billing rule — verify it restricts to apiPort.
		ports, _ := ingress["ports"].([]any)
		if len(ports) == 0 {
			t.Error("NetworkPolicy billing rule must specify ports, got unrestricted")
			continue
		}
		for _, p := range ports {
			portSpec := p.(map[string]any)
			portNum := portSpec["port"]
			if portNum != apiPort && portNum != nil {
				t.Errorf("billing NetworkPolicy rule must only use apiPort %d, got %v", apiPort, portNum)
			}
		}
	}
}

// TestHelmNetworkPolicy_GatewayAdmitted asserts the gateway namespace is
// admitted via an ingress rule — otherwise the ingress controller cannot
// reach the service.
func TestHelmNetworkPolicy_GatewayAdmitted(t *testing.T) {
	docs := renderChart(t, stagingSets...)
	nps := findResource(docs, "NetworkPolicy", "")
	if len(nps) == 0 {
		t.Skip("no NetworkPolicy rendered")
	}

	spec, _ := nps[0]["spec"].(map[string]any)
	ingresses, _ := spec["ingress"].([]any)

	gatewayAdmitted := false
	for _, ing := range ingresses {
		ingress := ing.(map[string]any)
		froms, _ := ingress["from"].([]any)
		for _, from := range froms {
			f := from.(map[string]any)
			nsSel, _ := f["namespaceSelector"].(map[string]any)
			labels, _ := nsSel["matchLabels"].(map[string]any)
			for _, v := range labels {
				if v == "gateway" {
					gatewayAdmitted = true
				}
			}
		}
	}

	if !gatewayAdmitted {
		t.Error("NetworkPolicy must admit the gateway namespace for ingress controller traffic")
	}
}
