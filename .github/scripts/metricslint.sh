#!/usr/bin/env bash
# Runs the full metrics lint suite:
#   1. Naming convention check (check-metric-naming.sh)
#   2. Registry lint (metrics-registry-lint.sh)
# Entry point called from validate-quality.yml.
set -euo pipefail

echo "::group::Metric naming check"
bash .github/scripts/check-metric-naming.sh
echo "::endgroup::"

echo "::group::Metrics registry lint"
bash .github/scripts/metrics-registry-lint.sh
echo "::endgroup::"

echo "  ✔  metrics lint passed"
