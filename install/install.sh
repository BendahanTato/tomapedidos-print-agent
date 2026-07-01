#!/usr/bin/env bash
# macOS / Linux quick-install script for the TomaPedidos Print Agent.
# Download the binary from GitHub Releases and install as a user service.
set -euo pipefail

REPO="tomapedidos/print-agent"
VERSION="${VERSION:-latest}"

if [ "$(uname -s)" = "Darwin" ]; then
  OS="darwin"
else
  OS="linux"
fi
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "unsupported arch: $ARCH"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/print-agent-${OS}-${ARCH}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/print-agent-${OS}-${ARCH}"
fi

echo "=== downloading print-agent ${VERSION} for ${OS}/${ARCH}"
curl -fsSL -o /tmp/print-agent "${URL}"
chmod +x /tmp/print-agent

echo "=== installing"
CONFIG_DIR="${HOME}/.config/tomapedidos"
mkdir -p "${CONFIG_DIR}"
CONFIG="${CONFIG_DIR}/printers.json"

if [ ! -f "${CONFIG}" ]; then
  /tmp/print-agent init-config --config "${CONFIG}"
  echo "=== starter config written to ${CONFIG} — edit it before starting"
fi

echo "=== registering as a service"
/tmp/print-agent install --config "${CONFIG}"

echo "=== done"
echo "Agent installed as a service and started."
echo "  Panel:   http://127.0.0.1:4510"
echo "  Config:  ${CONFIG}"
echo "  Logs:    ${CONFIG_DIR}/agent.log"
echo ""
echo "Manage:"
echo "  ${HOME}/.local/bin/print-agent start-svc"
echo "  ${HOME}/.local/bin/print-agent stop-svc"
echo "  ${HOME}/.local/bin/print-agent status-svc"
echo "  ${HOME}/.local/bin/print-agent uninstall"
