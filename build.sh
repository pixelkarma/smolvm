#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SMOLVM_DIR="$SCRIPT_DIR"
SMOLVM_HOME=${SMOLVM_HOME:-/root/.smolvm}
SMOLVM_BIN_DIR="$SMOLVM_HOME/bin"
SMOLVM_DATA_DIR=${SMOLVM_DATA_DIR:-$SMOLVM_HOME/data}
SMOLVM_CONFIG_PATH="$SMOLVM_HOME/smolvm.config.json"

if [ "$(id -u)" -ne 0 ]; then
  echo "build.sh must run as root" >&2
  exit 1
fi

if [ ! -f "$SMOLVM_DIR/go.mod" ]; then
  echo "expected smolvm sources at $SMOLVM_DIR" >&2
  exit 1
fi

if [ ! -f /etc/os-release ]; then
  echo "missing /etc/os-release" >&2
  exit 1
fi

. /etc/os-release

case "${ID:-}" in
  alpine) OS_FAMILY=alpine ;;
  ubuntu|debian) OS_FAMILY=debian ;;
  *)
    case "${ID_LIKE:-}" in
      *debian*) OS_FAMILY=debian ;;
      *) echo "unsupported OS: ${ID:-unknown}" >&2; exit 1 ;;
    esac
    ;;
esac

say() {
  printf '\n==> %s\n' "$1"
}

ensure_swap() {
  mem_kb=$(awk '/MemTotal:/ {print $2}' /proc/meminfo)
  swap_kb=$(awk '/SwapTotal:/ {print $2}' /proc/meminfo)
  min_mem_kb=$((2 * 1024 * 1024))
  desired_swap_mb=${SMOLVM_SWAP_MB:-4096}
  swapfile=${SMOLVM_SWAPFILE:-/swapfile-smolvm}

  if [ "${mem_kb:-0}" -ge "$min_mem_kb" ] || [ "${swap_kb:-0}" -gt 0 ]; then
    return
  fi

  say "Provisioning temporary swap for low-memory host"
  if [ ! -f "$swapfile" ]; then
    if command -v fallocate >/dev/null 2>&1; then
      fallocate -l "${desired_swap_mb}M" "$swapfile"
    else
      dd if=/dev/zero of="$swapfile" bs=1M count="$desired_swap_mb"
    fi
    chmod 600 "$swapfile"
    mkswap "$swapfile"
  fi
  if ! swapon --show=NAME | grep -qx "$swapfile"; then
    swapon "$swapfile"
  fi
}

detect_public_host() {
  if [ -n "${SMOLVM_PUBLIC_HOST:-}" ]; then
    printf '%s\n' "$SMOLVM_PUBLIC_HOST"
    return
  fi
  ip -4 route get 1.1.1.1 2>/dev/null | awk '/src/ {for (i = 1; i <= NF; i++) if ($i == "src") {print $(i+1); exit}}'
}

detect_agent_binary_name() {
  arch=$(uname -m)
  case "$arch" in
    aarch64|arm64) printf '%s\n' "smolagent-linux-aarch64" ;;
    x86_64|amd64) printf '%s\n' "smolagent-linux-x86" ;;
    *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
  esac
}

install_packages_alpine() {
  say "Installing Alpine prerequisites"
  apk update
  apk add bash build-base ca-certificates coreutils curl docker e2fsprogs git go sqlite tini util-linux
}

install_packages_debian() {
  say "Installing Debian/Ubuntu prerequisites"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends bash build-essential ca-certificates coreutils curl docker.io e2fsprogs git golang-go iproute2 sqlite3 tini util-linux
  apt-get clean
  rm -rf /var/lib/apt/lists/*
}

start_docker_alpine() {
  say "Enabling Docker"
  rc-update add docker default >/dev/null 2>&1 || true
  rc-service docker start
}

start_docker_debian() {
  say "Enabling Docker"
  systemctl enable docker
  systemctl restart docker
}

build_binaries() {
  say "Building smolvm binaries"
  mkdir -p "$SMOLVM_DIR/bin"
  arch=$(uname -m)
  case "$arch" in
    aarch64|arm64) goarch=arm64 ;;
    x86_64|amd64) goarch=amd64 ;;
    *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
  esac
  (cd "$SMOLVM_DIR" && go build -o "$SMOLVM_DIR/bin/smolvm-admin" ./)
  (cd "$SMOLVM_DIR" && GOOS=linux GOARCH="$goarch" go build -o "$SMOLVM_DIR/bin/$(detect_agent_binary_name)" ./cmd/smolagent)
}

deploy_binaries() {
  say "Deploying binaries"
  mkdir -p "$SMOLVM_BIN_DIR" "$SMOLVM_DATA_DIR"
  install -m 755 "$SMOLVM_DIR/bin/smolvm-admin" "$SMOLVM_BIN_DIR/smolvm-admin"
  install -m 755 "$SMOLVM_DIR/bin/$(detect_agent_binary_name)" "$SMOLVM_BIN_DIR/$(detect_agent_binary_name)"
}

write_config_file() {
  admin_password=${SMOLVM_ADMIN_PASSWORD:-changeme}
  public_host=$(detect_public_host)
  agent_binary_name=$(detect_agent_binary_name)
  mkdir -p "$SMOLVM_HOME"
  cat > "$SMOLVM_CONFIG_PATH" <<EOF
{
  "listen_addr": "${SMOLVM_LISTEN:-:8090}",
  "data_dir": "${SMOLVM_DATA_DIR}",
  "agent_binary_path": "${SMOLVM_BIN_DIR}/${agent_binary_name}",
  "image_name": "${SMOLVM_IMAGE:-smolvm-agent:latest}",
  "public_host": "${public_host}",
  "default_openai_api_key": "${SMOLVM_DEFAULT_OPENAI_API_KEY:-}",
  "admin_password": "${admin_password}"
}
EOF
  chmod 600 "$SMOLVM_CONFIG_PATH"
}

write_openrc_service() {
  cat > /etc/init.d/smolvm <<'EOF'
#!/sbin/openrc-run
name="smolvm"
description="smolvm admin"
command="/root/.smolvm/bin/smolvm-admin"
command_args="--config /root/.smolvm/smolvm.config.json"
command_background=true
pidfile="/run/smolvm.pid"
output_log="/var/log/smolvm/current.log"
error_log="/var/log/smolvm/current.log"

depend() {
  need docker
  after firewall
}

start_pre() {
  checkpath --directory --owner root:root --mode 0755 /root/.smolvm /root/.smolvm/data /var/log/smolvm
}
EOF
  chmod 755 /etc/init.d/smolvm
}

write_systemd_service() {
  cat > /etc/systemd/system/smolvm.service <<'EOF'
[Unit]
Description=smolvm admin
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
WorkingDirectory=/root/.smolvm
ExecStart=/root/.smolvm/bin/smolvm-admin --config /root/.smolvm/smolvm.config.json
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
}

configure_service_alpine() {
  say "Writing OpenRC service files"
  write_config_file
  write_openrc_service
  rc-update add smolvm default >/dev/null 2>&1 || true
}

configure_service_debian() {
  say "Writing systemd service files"
  write_config_file
  write_systemd_service
  systemctl daemon-reload
  systemctl enable smolvm
}

restart_service_alpine() {
  say "Restarting smolvm"
  if rc-service smolvm status >/dev/null 2>&1; then
    rc-service smolvm restart
  else
    rc-service smolvm start
  fi
}

restart_service_debian() {
  say "Restarting smolvm"
  systemctl restart smolvm
}

print_summary() {
  public_host=$(detect_public_host)
  echo "Admin URL: http://${public_host}:8090/login"
  echo "Config: $SMOLVM_CONFIG_PATH"
  if [ "$OS_FAMILY" = "alpine" ]; then
    echo "Logs: /var/log/smolvm/current.log"
  else
    echo "Logs: journalctl -u smolvm -f"
  fi
}

case "$OS_FAMILY" in
  alpine)
    install_packages_alpine
    start_docker_alpine
    ;;
  debian)
    install_packages_debian
    start_docker_debian
    ;;
esac

ensure_swap
build_binaries
deploy_binaries

case "$OS_FAMILY" in
  alpine)
    configure_service_alpine
    restart_service_alpine
    ;;
  debian)
    configure_service_debian
    restart_service_debian
    ;;
esac

say "Done"
print_summary
