#!/usr/bin/env bash
# Minimal post-deploy smoke test: start the server image against docker-compose
# dependencies and hit /readyz + a sample read endpoint.
# Runs in CI after build-image on the built image tag.
set -euo pipefail

: "${IMAGE_TAG:?IMAGE_TAG is required (e.g. ghcr.io/bcbp-solutions-fzc-llc/commercial-entitlements:sha-abc1234)}"

CONTAINER_NAME="commercial-entitlements-smoke"
PORT=18080

cleanup() {
  docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

echo "::group::Smoke test — start container"
docker run -d --name "$CONTAINER_NAME" \
  --network host \
  -e APP_ENV=dev \
  -e DATABASE_URL="${DATABASE_URL:-postgres://app:app@localhost:5432/commercial_entitlements?sslmode=disable}" \
  -e VALKEY_URL="${VALKEY_URL:-localhost:6379}" \
  -e IAM_CATALOG_BASE_URL="${IAM_CATALOG_BASE_URL:-http://localhost:8081}" \
  -e RUN_MIGRATIONS=false \
  -e APP_PORT="$PORT" \
  "$IMAGE_TAG"
echo "::endgroup::"

echo "::group::Smoke test — wait for /readyz"
for i in $(seq 1 30); do
  if curl -sf "http://localhost:${PORT}/readyz" >/dev/null 2>&1; then
    echo "  ✔  /readyz OK (attempt ${i})"
    break
  fi
  echo "  waiting (${i}/30)..."
  sleep 2
  if [ "$i" -eq 30 ]; then
    echo "::error::Server did not become ready in 60 s"
    docker logs "$CONTAINER_NAME"
    exit 1
  fi
done
echo "::endgroup::"

echo "::group::Smoke test — /healthz"
curl -sf "http://localhost:${PORT}/healthz" | jq .
echo "::endgroup::"

echo "  ✔  smoke tests passed"
