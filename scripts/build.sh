#!/usr/bin/env bash
set -euo pipefail

# Cross-compile the print-agent binary for all supported platforms.
# Output goes to dist/.
#
# Usage:
#   ./scripts/build.sh              build all targets
#   ./scripts/build.sh darwin/arm64  build a single target
#
# Requires: Go 1.22+ (the go.mod enforces 1.25 via modernc.org/sqlite).
# CGO is disabled — the binary is 100% Go.

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo '0.1.0-dev')}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')}"
BUILDTIME="${BUILDTIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
BASE_LDFLAGS="-X github.com/tomapedidos/print-agent/internal/version.Version=${VERSION} -X github.com/tomapedidos/print-agent/internal/version.Commit=${COMMIT} -X github.com/tomapedidos/print-agent/internal/version.BuildTime=${BUILDTIME}"

mkdir -p dist

TARGETS=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "windows/arm64"
)

build_one() {
  local target="$1"
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  local out="dist/print-agent-${GOOS}-${GOARCH}"
  local ldflags="${BASE_LDFLAGS} -s -w"
  if [ "${GOOS}" = "windows" ]; then
    out="${out}.exe"
    # Preservar símbolos en Windows para evitar falsos positivos de antivirus (Avast IDP.Generic / Defender)
    ldflags="${BASE_LDFLAGS}"
    local resource="cmd/print-agent/resource_windows_${GOARCH}.syso"
    rm -f "${resource}"
    if command -v goversioninfo >/dev/null 2>&1; then
      local major=0 minor=0 patch=0 build=0 file_version="0.0.0.0"
      if [[ "${VERSION}" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+)(\.([0-9]+))?$ ]]; then
        major="${BASH_REMATCH[1]}"
        minor="${BASH_REMATCH[2]}"
        patch="${BASH_REMATCH[3]}"
        build="${BASH_REMATCH[5]:-0}"
        file_version="${major}.${minor}.${patch}.${build}"
      fi
      echo "  -> generating Windows VERSIONINFO for ${GOARCH}"
      GOARCH="${GOARCH}" goversioninfo \
        -o "${resource}" \
        -manifest cmd/print-agent/manifest.xml \
        -ver-major "${major}" -ver-minor "${minor}" -ver-patch "${patch}" -ver-build "${build}" \
        -product-ver-major "${major}" -product-ver-minor "${minor}" -product-ver-patch "${patch}" -product-ver-build "${build}" \
        -file-version "${file_version}" -product-version "${file_version}" \
        cmd/print-agent/versioninfo.json
    else
      echo "  !! goversioninfo not found; building Windows binary without VERSIONINFO/manifest resources" >&2
    fi
  fi
  echo "  -> ${out}"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -buildvcs=false -trimpath -ldflags "${ldflags}" -o "${out}" ./cmd/print-agent
}

echo "=== cross-compile ${VERSION}"
if [ $# -gt 0 ]; then
  for t in "$@"; do build_one "$t"; done
else
  for t in "${TARGETS[@]}"; do build_one "$t"; done
fi

echo "=== sha256sums"
(cd dist && shasum -a 256 *) > dist/SHA256SUMS
cat dist/SHA256SUMS
echo "=== done"
ls -lh dist/
