#!/usr/bin/env bash
# Helm lint + template + kubeconform validation for the commercial-entitlements chart.
#
# Values combinations exercised:
#   base | base+dev | base+prod
#
# kubeconform validates standard K8s resources (K8s 1.31.0 schemas).
# CRD kinds (ServiceMonitor, PrometheusRule, HTTPRoute, SecurityPolicy,
# AuthorizationPolicy) are skipped via -ignore-missing-schemas — these
# are vendor CRDs whose schemas are not in the upstream K8s schema repo;
# their structure is validated separately by the platform team's CRD registry.
set -euo pipefail

CHART_DIR="deploy/helm"
BASE_VALUES="${CHART_DIR}/values.yaml"
HELM_VERSION="v3.16.3"
KUBECONFORM_VERSION="v0.6.7"
K8S_VERSION="1.31.0"
RELEASE_NAME="commercial-entitlements"
NAMESPACE="commercial"

# Shared --set overrides applied to both helm lint and helm template.
# existingSecret=ci-lint-skip: suppresses Secret resource generation so
# chart validation does not fail on missing secretValues keys — mirrors
# the production deployment model where existingSecret always points to
# a pre-provisioned Secret.
SHARED_SETS=(
  "--set" "image.tag=ci-check"
  "--set" "existingSecret=ci-lint-skip"
)

# ── Install tools if absent ───────────────────────────────────────────────
# In CI, ~/go/bin is cached by the validate-quality.yml action cache step.
echo "::group::Tool setup"
if ! command -v helm >/dev/null 2>&1; then
  echo "helm not found — installing ${HELM_VERSION} via go install..."
  go install "helm.sh/helm/v3/cmd/helm@${HELM_VERSION}"
fi
echo "helm: $(helm version --short 2>/dev/null)"

if ! command -v kubeconform >/dev/null 2>&1; then
  echo "kubeconform not found — installing ${KUBECONFORM_VERSION} via go install..."
  go install "github.com/yannh/kubeconform/cmd/kubeconform@${KUBECONFORM_VERSION}"
fi
KC_VER=$(kubeconform -v 2>/dev/null || echo "installed (version flag unsupported)")
echo "kubeconform: ${KC_VER}"
echo "::endgroup::"

# ── Step 1: helm lint ─────────────────────────────────────────────────────
echo "::group::helm lint"
helm lint "${CHART_DIR}" "${SHARED_SETS[@]}"
echo "::endgroup::"
echo "  ✔  helm lint passed"

# ── Step 2: helm template + kubeconform for each values combination ───────
FAILED=0

run_combo() {
  local label="$1"; shift
  echo "::group::helm template + kubeconform [${label}]"

  local rendered
  if ! rendered=$(helm template "${RELEASE_NAME}" "${CHART_DIR}" \
      "${SHARED_SETS[@]}" \
      --namespace "${NAMESPACE}" \
      -f "${BASE_VALUES}" \
      "$@" 2>&1); then
    echo "::error file=${CHART_DIR},title=helm template [${label}]::helm template failed for [${label}] — output below"
    printf '%s\n' "${rendered}"
    FAILED=1
    echo "::endgroup::"
    return
  fi

  if ! printf '%s\n' "${rendered}" \
      | kubeconform \
          -strict \
          -summary \
          -ignore-missing-schemas \
          -kubernetes-version "${K8S_VERSION}" \
          -output pretty; then
    echo "::error file=${CHART_DIR},title=kubeconform [${label}]::Manifest validation failed for [${label}] — see output above"
    FAILED=1
  fi

  echo "::endgroup::"
}

run_combo "base"
run_combo "base+dev"  -f "${CHART_DIR}/values-dev.yaml"
run_combo "base+prod" -f "${CHART_DIR}/values-prod.yaml"

# ── Final result ──────────────────────────────────────────────────────────
if [ "${FAILED}" -ne 0 ]; then
  echo ""
  echo "::error::Helm check failed — see grouped output above for details."
  exit 1
fi

echo ""
echo "  ✔  helm check passed: base | base+dev | base+prod"
echo "  ⚠  CRD schema coverage: ServiceMonitor, PrometheusRule, HTTPRoute,"
echo "     SecurityPolicy, AuthorizationPolicy were SKIPPED (no org-wide"
echo "     CRD schema catalog configured; -ignore-missing-schemas active)."
