#!/usr/bin/env bash
# Cross-compiles release binaries for all target platforms and writes per-binary
# checksums. The artifact directory is the working directory when this runs.
#
# Produces for each platform:
#   commercial-entitlements_{version}_{os}_{arch}[.exe]
#   commercial-entitlements_{version}_{os}_{arch}[.exe].sha256
set -euo pipefail

: "${RELEASE_TAG:?RELEASE_TAG is required}"

TARGETS=(
  "linux   amd64"
  "linux   arm64"
  "darwin  amd64"
  "darwin  arm64"
  "windows amd64"
)

echo "Building release binaries for tag ${RELEASE_TAG}"

for target in "${TARGETS[@]}"; do
  read -r GOOS GOARCH <<< "$target"
  EXT=""
  [ "${GOOS}" = "windows" ] && EXT=".exe"
  ASSET_NAME="commercial-entitlements_${RELEASE_TAG}_${GOOS}_${GOARCH}${EXT}"

  echo "  -> ${GOOS}/${GOARCH}"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath \
      -ldflags "-s -w -X main.buildVersion=${RELEASE_TAG}" \
      -o "${ASSET_NAME}" \
      ./cmd/server

  chmod +x "${ASSET_NAME}"
  sha256sum "${ASSET_NAME}" > "${ASSET_NAME}.sha256"
  echo "    ✔  ${ASSET_NAME}"
done

echo "commercial-entitlements_${RELEASE_TAG}_linux_amd64" > release-asset.name
echo "All binaries prepared."
