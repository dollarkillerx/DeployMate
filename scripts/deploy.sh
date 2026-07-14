#!/bin/sh
# DeployMate one-file installer.
#
# Self-contained: downloads the agent binary from GitHub Releases and embeds the
# systemd unit, so no repository checkout is required. Run as root on a Linux
# host with systemd.
#
#   curl -fsSL https://raw.githubusercontent.com/dollarkillerx/DeployMate/main/scripts/deploy.sh | sudo sh
#
# or download and run:
#
#   sudo DEPLOYMATE_PUBLIC_HOST=203.0.113.10 ./deploy.sh install
#
set -eu

ACTION="${1:-install}"
VERSION="${DEPLOYMATE_VERSION:-v0.0.2}"
DOWNLOAD_URL="${DEPLOYMATE_DOWNLOAD_URL:-https://github.com/dollarkillerx/DeployMate/releases/download/$VERSION/deploymate-agent}"
DOWNLOAD_SHA256="${DEPLOYMATE_DOWNLOAD_SHA256:-}"

PREFIX="${PREFIX:-/usr/local}"
CONFIG_DIR="${CONFIG_DIR:-/etc/deploymate}"
STATE_DIR="/var/lib/deploymate"
UNIT_PATH="${UNIT_PATH:-/etc/systemd/system/deploymate-agent.service}"
BINARY_PATH="$PREFIX/bin/deploymate-agent"
LISTEN="${DEPLOYMATE_LISTEN:-0.0.0.0:9443}"

detect_public_ipv4() {
  for url in "https://api.ipify.org" "https://ifconfig.me/ip" "https://ipinfo.io/ip" "https://checkip.amazonaws.com"; do
    if command -v curl >/dev/null 2>&1; then
      ip=$(curl -fsS -4 --max-time 5 "$url" 2>/dev/null | tr -d '[:space:]')
    elif command -v wget >/dev/null 2>&1; then
      ip=$(wget -qO- --timeout=5 "$url" 2>/dev/null | tr -d '[:space:]')
    else
      break
    fi
    case "$ip" in
      *[!0-9.]*|"") ;;
      *.*.*.*) printf '%s' "$ip"; return 0 ;;
    esac
  done
  return 1
}

resolve_public_host() {
  if [ -n "${DEPLOYMATE_PUBLIC_HOST:-}" ]; then
    printf '%s' "$DEPLOYMATE_PUBLIC_HOST"
    return
  fi
  if ip=$(detect_public_ipv4); then
    echo "Detected public IPv4: $ip" >&2
    printf '%s' "$ip"
    return
  fi
  echo "Could not detect public IPv4; falling back to hostname" >&2
  hostname -f 2>/dev/null || hostname
}

PUBLIC_HOST=$(resolve_public_host)

require_root() {
  if [ "$(id -u)" -ne 0 ]; then echo "DeployMate installation must run as root" >&2; exit 1; fi
  if [ "$(uname -s)" != "Linux" ]; then echo "DeployMate Agent supports Linux only" >&2; exit 1; fi
  command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }
}

download_binary() {
  tmp=$(mktemp)
  echo "Downloading $DOWNLOAD_URL" >&2
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 -o "$tmp" "$DOWNLOAD_URL"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$tmp" "$DOWNLOAD_URL"
  else
    echo "curl or wget is required to download the agent binary" >&2
    rm -f "$tmp"
    exit 1
  fi
  if [ -n "$DOWNLOAD_SHA256" ]; then
    echo "$DOWNLOAD_SHA256  $tmp" | sha256sum -c - >/dev/null 2>&1 || {
      echo "checksum verification failed for downloaded binary" >&2
      rm -f "$tmp"
      exit 1
    }
    echo "Checksum verified" >&2
  fi
  install -m 755 "$tmp" "$BINARY_PATH"
  rm -f "$tmp"
}

write_unit() {
  cat >"$UNIT_PATH" <<EOF
[Unit]
Description=DeployMate remote MCP agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
UMask=0077
ExecStart=$BINARY_PATH -config $CONFIG_DIR/agent.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
StateDirectory=deploymate
NoNewPrivileges=false

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$UNIT_PATH"
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

is_installed() {
  [ -f "$UNIT_PATH" ] || [ -x "$BINARY_PATH" ]
}

install_agent() {
  require_root
  if is_installed; then
    echo "Existing installation detected; updating to latest binary" >&2
    if systemctl is-active --quiet deploymate-agent.service; then
      echo "Stopping running agent" >&2
      systemctl stop deploymate-agent.service
    fi
    download_binary
    write_config
    write_unit
    systemctl daemon-reload
    systemctl enable --now deploymate-agent.service
    echo "Agent updated and restarted" >&2
    print_client_config
    return
  fi
  echo "No existing installation found; performing fresh install" >&2
  install -d -m 700 "$CONFIG_DIR" "$CONFIG_DIR/tls" "$STATE_DIR"
  download_binary
  write_config
  write_unit
  systemctl daemon-reload
  systemctl enable --now deploymate-agent.service
  print_client_config
}

print_client_config() {
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
  upgrade) require_root; download_binary; systemctl restart deploymate-agent.service ;;
  rotate-token) require_root; "$BINARY_PATH" rotate-token -config "$CONFIG_DIR/agent.yaml"; systemctl restart deploymate-agent.service; echo "New token: $(tr -d '\n' <"$CONFIG_DIR/initial-token")" ;;
  uninstall) require_root; systemctl disable --now deploymate-agent.service 2>/dev/null || true; rm -f "$UNIT_PATH" "$BINARY_PATH"; systemctl daemon-reload; echo "Configuration and state were preserved in $CONFIG_DIR and $STATE_DIR" ;;
  *) echo "usage: $0 {install|upgrade|rotate-token|uninstall}" >&2; exit 2 ;;
esac
