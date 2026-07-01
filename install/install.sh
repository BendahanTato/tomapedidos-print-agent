#!/usr/bin/env bash
# macOS / Linux quick-install script for the TomaPedidos Print Agent.
# Download the binary from GitHub Releases and install as a user service.
set -euo pipefail

REPO="BendahanTato/tomapedidos-print-agent"
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

BIN_DIR="${HOME}/.local/bin"
mkdir -p "${BIN_DIR}"
BIN="${BIN_DIR}/print-agent"

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/print-agent-${OS}-${ARCH}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/print-agent-${OS}-${ARCH}"
fi

echo "=== downloading print-agent ${VERSION} for ${OS}/${ARCH}"
curl -fsSL -o "${BIN}" "${URL}"
chmod +x "${BIN}"

echo "=== installing"
CONFIG_DIR="${HOME}/.config/tomapedidos"
mkdir -p "${CONFIG_DIR}"
CONFIG="${CONFIG_DIR}/printers.json"

if [ ! -f "${CONFIG}" ]; then
  "${BIN}" init-config --config "${CONFIG}"
  echo "=== starter config written to ${CONFIG} — edit it before starting"
fi

# Fix the default persist_path to use an absolute path so launchd/systemd
# (which run from /) can open the SQLite database.
if command -v python3 &>/dev/null; then
  python3 -c "
import json, sys
with open('${CONFIG}') as f: cfg = json.load(f)
cfg['queue']['persist_path'] = '${CONFIG_DIR}/jobs.db'
with open('${CONFIG}', 'w') as f: json.dump(cfg, f, indent=2)
" 2>/dev/null || true
fi

echo "=== registering as a service"
"${BIN}" install --config "${CONFIG}"

echo "=== done"
echo "Agent installed as a service and started."
echo "  Panel:   http://127.0.0.1:4510"
echo "  Config:  ${CONFIG}"
echo "  Logs:    ${CONFIG_DIR}/agent.log"
echo "  Binary:  ${BIN}"
echo ""
echo "Manage:"
echo "  ${BIN} status-svc"
echo "  ${BIN} stop-svc"
echo "  ${BIN} start-svc"
echo "  ${BIN} uninstall"
echo ""
echo "Add to PATH:"
echo "  echo 'export PATH=\"\${HOME}/.local/bin:\${PATH}\"' >> ~/.zshrc"
echo "  source ~/.zshrc"
