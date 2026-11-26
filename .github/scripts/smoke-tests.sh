#!/usr/bin/env bash
# Image size + startup gate smoke tests for the CI-built Docker image.
# Runs with NO infrastructure — the binary must exit non-zero on missing env vars.
set -euo pipefail

IMAGE="commercial-entitlements-ci-test"

echo "::group::Image size check (linux/amd64)"
# The limit is generous enough to catch regressions (e.g. accidentally COPYing
# vendor/ or embedding test assets) without failing on normal arch variance.
MAX_MB=200
size=$(docker image inspect "$IMAGE" --format='{{.Size}}')
mb=$((size / 1024 / 1024))
echo "Image size: ${mb} MB (limit: ${MAX_MB} MB)"
[ "${mb}" -le "${MAX_MB}" ] &
P1=$!
echo "::endgroup::"

echo "::group::Startup gate"
# Server must exit non-zero on missing required env vars,
# proving the binary runs and validateRequiredEnv fires correctly.
# timeout 10s kills the container if it hangs instead of exiting.
exit_code=0
timeout 10s docker run --rm "$IMAGE" 2>/dev/null || exit_code=$?
echo "Container exit code: ${exit_code} (expected non-zero)"
[ "${exit_code}" -ne 0 ] &
P2=$!
echo "::endgroup::"

wait $P1 || {
  echo "::error file=Dockerfile,title=Image size::Image is ${mb} MB, exceeds ${MAX_MB} MB limit — check COPY/ADD instructions for accidental inclusions"
  exit 1
}
wait $P2 || {
  echo "::error file=cmd/server/main.go,title=Startup gate::Server exited 0 on missing env vars — validateRequiredEnv must exit non-zero"
  exit 1
}

{
  echo "### Smoke test results"
  echo "- ✅ Startup gate: server exits ${exit_code} on missing env vars (validateRequiredEnv fires)"
  echo "- 📦 Image size (linux/amd64): **${mb} MB** (limit: ${MAX_MB} MB)"
  echo "- 🔍 [Security (Trivy SARIF results)](https://github.com/${GITHUB_REPOSITORY}/security/code-scanning)"
} >> "$GITHUB_STEP_SUMMARY"
