#!/bin/sh
set -eu

ACTION="${1:-install}"
PREFIX="${PREFIX:-/usr/local}"
CONFIG_DIR="${CONFIG_DIR:-/etc/deploymate}"
STATE_DIR="/var/lib/deploymate"
UNIT_PATH="${UNIT_PATH:-/etc/systemd/system/deploymate-agent.service}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BINARY_SOURCE="${DEPLOYMATE_BINARY:-$SCRIPT_DIR/../deploymate-agent}"
PUBLIC_HOST="${DEPLOYMATE_PUBLIC_HOST:-$(hostname -f 2>/dev/null || hostname)}"
LISTEN="${DEPLOYMATE_LISTEN:-0.0.0.0:9443}"

require_root() {
  if [ "$(id -u)" -ne 0 ]; then echo "DeployMate installation must run as root" >&2; exit 1; fi
  if [ "$(uname -s)" != "Linux" ]; then echo "DeployMate Agent supports Linux only" >&2; exit 1; fi
  command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }
}

write_config() {
  if [ ! -f "$CONFIG_DIR/agent.yaml" ]; then
    cat >"$CONFIG_DIR/agent.yaml" <<EOF
listen: $LISTEN
tls:
  certificate_file: $CONFIG_DIR/tls/server.crt
  private_key_file: $CONFIG_DIR/tls/server.key
  auto_generate: true
  hosts:
    - $PUBLIC_HOST
auth:
  token_hash_file: $CONFIG_DIR/token.sha256
  initial_token_file: $CONFIG_DIR/initial-token
limits:
  max_concurrent_commands: 4
  max_file_size: 104857600
  max_timeout_seconds: 86400
  transfer_ticket_ttl_seconds: 600
  http_read_timeout_seconds: 1800
EOF
    chmod 600 "$CONFIG_DIR/agent.yaml"
  fi
}

install_agent() {
  require_root
  [ -x "$BINARY_SOURCE" ] || { echo "binary not found or not executable: $BINARY_SOURCE" >&2; exit 1; }
  install -d -m 700 "$CONFIG_DIR" "$CONFIG_DIR/tls" "$STATE_DIR"
  install -m 755 "$BINARY_SOURCE" "$PREFIX/bin/deploymate-agent"
  write_config
  install -m 644 "$SCRIPT_DIR/../packaging/systemd/deploymate-agent.service" "$UNIT_PATH"
  systemctl daemon-reload
  systemctl enable --now deploymate-agent.service
  attempts=0
  while [ ! -s "$CONFIG_DIR/initial-token" ] && [ "$attempts" -lt 20 ]; do
    attempts=$((attempts + 1))
    sleep 1
  done
  if [ ! -s "$CONFIG_DIR/initial-token" ]; then
    systemctl status deploymate-agent.service --no-pager >&2 || true
    echo "Agent did not create its initial token" >&2
    exit 1
  fi
  TOKEN=$(tr -d '\n' <"$CONFIG_DIR/initial-token")
  HOST_PORT=$(printf '%s' "$LISTEN" | sed "s/^0.0.0.0/$PUBLIC_HOST/")
  cat <<EOF
servers:
  $(hostname -s):
    url: https://$HOST_PORT/mcp
    token: $TOKEN
    insecure_skip_verify: true
EOF
}

case "$ACTION" in
  install) install_agent ;;
  upgrade) require_root; [ -x "$BINARY_SOURCE" ] || { echo "binary not found: $BINARY_SOURCE" >&2; exit 1; }; install -m 755 "$BINARY_SOURCE" "$PREFIX/bin/deploymate-agent"; systemctl restart deploymate-agent.service ;;
  rotate-token) require_root; "$PREFIX/bin/deploymate-agent" rotate-token -config "$CONFIG_DIR/agent.yaml"; systemctl restart deploymate-agent.service; echo "New token: $(tr -d '\n' <"$CONFIG_DIR/initial-token")" ;;
  uninstall) require_root; systemctl disable --now deploymate-agent.service 2>/dev/null || true; rm -f "$UNIT_PATH" "$PREFIX/bin/deploymate-agent"; systemctl daemon-reload; echo "Configuration and state were preserved in $CONFIG_DIR and $STATE_DIR" ;;
  *) echo "usage: $0 {install|upgrade|rotate-token|uninstall}" >&2; exit 2 ;;
esac
