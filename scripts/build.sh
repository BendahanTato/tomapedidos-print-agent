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
LDFLAGS="-X github.com/tomapedidos/print-agent/internal/version.Version=${VERSION} -X github.com/tomapedidos/print-agent/internal/version.Commit=${COMMIT} -X github.com/tomapedidos/print-agent/internal/version.BuildTime=${BUILDTIME} -s -w"

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
  if [ "${GOOS}" = "windows" ]; then out="${out}.exe"; fi
  echo "  -> ${out}"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${out}" ./cmd/print-agent
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
