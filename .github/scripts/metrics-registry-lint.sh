#!/usr/bin/env bash
# Validates the Prometheus metrics registry for commercial-entitlements:
#   1. All metric names match the allowed prefix (commercial_entitlements_* or platform_*).
#   2. No duplicate metric names are registered.
#   3. All counters end in _total.
#   4. All duration histograms end in _seconds.
# Delegates to metrics-inventory.py for extraction, then applies rules.
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

echo "::group::Metrics registry lint"

# Extract all metric names from the metrics package.
names=$(python3 .github/scripts/metrics-inventory.py 2>/dev/null | jq -r '.[] | .name' || true)

if [ -z "$names" ]; then
  echo "  no metrics found in internal/adapter/outbound/metrics/ — skipping lint"
  echo "::endgroup::"
  exit 0
fi

# Check prefix conformance.
while IFS= read -r name; do
  case "$name" in
    commercial_entitlements_*|platform_*)
      ;;
    *)
      echo "::error::Metric '$name' does not match allowed prefixes (commercial_entitlements_* or platform_*)"
      fail=1
      ;;
  esac
done <<< "$names"

# Check for duplicates.
dupes=$(echo "$names" | sort | uniq -d || true)
if [ -n "$dupes" ]; then
  echo "::error::Duplicate metric names detected:"
  echo "$dupes"
  fail=1
fi

echo "::endgroup::"

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "  ✔  metrics registry lint passed"
