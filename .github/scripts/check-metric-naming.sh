#!/usr/bin/env bash
# Validates that all Prometheus metric names registered by this service follow
# the Enterprise Platform Observability Standard naming hierarchy:
#   commercial_entitlements_*   own service metrics
#   platform_*                  shared platform metrics (via platform-gincommon)
# No other prefix is permitted. Metric names are extracted from the Go source
# (prometheus.NewCounter*, NewGauge*, NewHistogram*, NewSummary* calls).
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

# Extract metric name strings from prometheus constructor calls.
names=$(grep -rEoh 'Name:[[:space:]]*"[^"]+"' --include='*.go' internal/adapter/outbound/metrics/ 2>/dev/null \
        | grep -oE '"[^"]+"' | tr -d '"' | sort -u || true)

if [ -z "$names" ]; then
  echo "  ✔  no metrics registered — nothing to check"
  exit 0
fi

while IFS= read -r name; do
  case "$name" in
    commercial_entitlements_*|platform_*)
      ;;
    *)
      echo "::error file=internal/adapter/outbound/metrics/::Metric name '$name' does not match the allowed prefixes (commercial_entitlements_* or platform_*). Update the metric name to conform to the Platform Observability Standard."
      fail=1
      ;;
  esac
done <<< "$names"

# Suffix rules: counters must end in _total; durations in _seconds.
while IFS= read -r name; do
  # Detect counter descriptors.
  if grep -rqE "NewCounterVec[[:space:]]*\([[:space:]]*prometheus\.CounterOpts\{[^}]*Name:[[:space:]]*\"${name}\"" \
       --include='*.go' internal/adapter/outbound/metrics/ 2>/dev/null; then
    if [[ "$name" != *_total ]]; then
      echo "::error file=internal/adapter/outbound/metrics/::Counter metric '$name' must end in _total (Observability Standard)."
      fail=1
    fi
  fi
done <<< "$names"

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "  ✔  metric naming check passed"
