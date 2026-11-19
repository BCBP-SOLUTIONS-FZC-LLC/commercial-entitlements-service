#!/usr/bin/env bash
# Helm lint + template + kubeconform validation for the commercial-entitlements chart.
#
# Values combinations exercised:
#   base only | base+values-dev.yaml | base+values-prod.yaml
#
# kubeconform validates standard K8s resources (K8s 1.31.0 schemas).
# CRD kinds (ServiceMonitor, PrometheusRule, HTTPRoute, SecurityPolicy,
# AuthorizationPolicy) are skipped via -ignore-missing-schemas — these
# are vendor CRDs whose schemas are not in the upstream K8s schema repo;
# their structure is validated separately by the platform team's CRD registry.
set -euo pipefail

cd "$(dirname "$0")/../.."

CHART="deploy/helm"
K8S_VERSION="1.31.0"

# Install tools if not present (cached between runs in CI via actions/cache).
if ! command -v helm >/dev/null; then
  echo "Installing helm..."
  curl -sSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | \
    DESIRED_VERSION=v3.16.3 bash
fi
if ! command -v kubeconform >/dev/null; then
  echo "Installing kubeconform..."
  go install github.com/yannh/kubeconform/cmd/kubeconform@v0.6.7
fi

KUBECONFORM_FLAGS=(
  -strict
  -kubernetes-version "$K8S_VERSION"
  -ignore-missing-schemas
  -summary
)

fail=0

run_check() {
  local label="$1"
  shift
  echo "::group::helm lint — ${label}"
  helm lint --strict "$CHART" "$@" || { fail=1; echo "::endgroup::"; return; }
  echo "::endgroup::"

  echo "::group::helm template + kubeconform — ${label}"
  helm template commercial-entitlements "$CHART" "$@" \
    | kubeconform "${KUBECONFORM_FLAGS[@]}" || { fail=1; echo "::endgroup::"; return; }
  echo "::endgroup::"
}

run_check "base only"
run_check "base+values-dev"  -f "${CHART}/values-dev.yaml"
run_check "base+values-prod" -f "${CHART}/values-prod.yaml"

if [ "$fail" -ne 0 ]; then
  echo "::error::Helm check failed — see groups above for details."
  exit 1
fi
echo "  ✔  helm check passed (lint + template + kubeconform, 3 value combos)"
