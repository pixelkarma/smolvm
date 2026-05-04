#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SMOLVM_DIR="$SCRIPT_DIR"

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

detect_host_home() {
  if [ -n "${SMOLVM_HOME:-}" ]; then
    printf '%s\n' "$SMOLVM_HOME"
    return
  fi
  if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
    user_home=$(getent passwd "$SUDO_USER" | awk -F: '{print $6}')
    if [ -n "$user_home" ]; then
      printf '%s/.smolvm\n' "$user_home"
      return
    fi
  fi
  if [ -n "${HOME:-}" ]; then
    printf '%s/.smolvm\n' "$HOME"
    return
  fi
  printf '%s\n' "/root/.smolvm"
}

SMOLVM_HOME=$(detect_host_home)
SMOLVM_BIN_DIR="$SMOLVM_HOME/bin"
SMOLVM_DATA_DIR=${SMOLVM_DATA_DIR:-$SMOLVM_HOME/data}
SMOLVM_CONFIG_PATH="$SMOLVM_HOME/smolvm.config.json"
SMOLVM_ASSETS_DIR="$SMOLVM_HOME/assets"

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

detect_outbound_interface() {
  ip -4 route get 1.1.1.1 2>/dev/null | awk '/dev/ {for (i = 1; i <= NF; i++) if ($i == "dev") {print $(i+1); exit}}'
}

resolve_default_openai_key() {
  if [ -n "${SMOLVM_DEFAULT_OPENAI_API_KEY:-}" ]; then
    printf '%s\n' "$SMOLVM_DEFAULT_OPENAI_API_KEY"
    return
  fi
  key_file="${SMOLVM_KEY_FILE:-$HOME/.openai}"
  if [ -f "$key_file" ]; then
    first_line=$(head -n 1 "$key_file" | tr -d '\r\n')
    case "$first_line" in
      OPENAI_API_KEY=*) printf '%s\n' "${first_line#OPENAI_API_KEY=}" ;;
      *) printf '%s\n' "$first_line" ;;
    esac
  fi
}

detect_agent_binary_name() {
  arch=$(uname -m)
  case "$arch" in
    aarch64|arm64) printf '%s\n' "smolagent-linux-aarch64" ;;
    x86_64|amd64) printf '%s\n' "smolagent-linux-x86" ;;
    *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
  esac
}

detect_firecracker_arch() {
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) printf '%s\n' "x86_64" ;;
    *) echo "unsupported architecture for firecracker: $arch" >&2; exit 1 ;;
  esac
}

install_packages_alpine() {
  say "Installing Alpine prerequisites"
  apk update
  apk add bash build-base ca-certificates coreutils curl e2fsprogs git go iptables iproute2 socat sqlite tini util-linux
}

install_packages_debian() {
  say "Installing Debian/Ubuntu prerequisites"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends bash build-essential ca-certificates coreutils curl e2fsprogs git golang-go iproute2 iptables socat sqlite3 tini util-linux
  apt-get clean
  rm -rf /var/lib/apt/lists/*
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
  (cd "$SMOLVM_DIR" && CGO_ENABLED=0 go build -buildvcs=false -o "$SMOLVM_DIR/bin/smolvm-admin" ./)
  (cd "$SMOLVM_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -buildvcs=false -o "$SMOLVM_DIR/bin/$(detect_agent_binary_name)" ./cmd/smolagent)
}

deploy_binaries() {
  say "Deploying binaries"
  mkdir -p "$SMOLVM_BIN_DIR" "$SMOLVM_DATA_DIR" "$SMOLVM_ASSETS_DIR"
  install -m 755 "$SMOLVM_DIR/bin/smolvm-admin" "$SMOLVM_BIN_DIR/smolvm-admin"
  install -m 755 "$SMOLVM_DIR/bin/$(detect_agent_binary_name)" "$SMOLVM_BIN_DIR/$(detect_agent_binary_name)"
}

download_firecracker() {
  say "Installing Firecracker binary"
  fc_arch=$(detect_firecracker_arch)
  release_tag=$(curl -fsSL https://api.github.com/repos/firecracker-microvm/firecracker/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
  if [ -z "$release_tag" ]; then
    echo "failed to determine latest Firecracker release" >&2
    exit 1
  fi
  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT INT TERM
  archive="$tmpdir/firecracker.tgz"
  curl -fsSL -o "$archive" "https://github.com/firecracker-microvm/firecracker/releases/download/${release_tag}/firecracker-${release_tag}-${fc_arch}.tgz"
  tar -xzf "$archive" -C "$tmpdir"
  fc_bin=$(find "$tmpdir" -type f -name 'firecracker-*' ! -name '*.json' ! -name '*.yaml' ! -name '*.sig' ! -name '*.debug' | head -n 1)
  if [ -z "$fc_bin" ]; then
    echo "failed to extract Firecracker binary" >&2
    exit 1
  fi
  install -m 755 "$fc_bin" "$SMOLVM_BIN_DIR/firecracker"
  rm -rf "$tmpdir"
  trap - EXIT INT TERM
}

download_guest_assets() {
  say "Downloading Alpine guest assets"
  curl -fsSL -o "$SMOLVM_ASSETS_DIR/vmlinux.bin" \
    "https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/kernels/vmlinux.bin"
  curl -fsSL -o "$SMOLVM_ASSETS_DIR/alpine-minirootfs.tar.gz" \
    "https://dl-cdn.alpinelinux.org/alpine/v3.21/releases/x86_64/alpine-minirootfs-3.21.2-x86_64.tar.gz"
}

cleanup_old_docker_artifacts() {
  say "Cleaning prior Docker-based smolvm artifacts"
  case "$OS_FAMILY" in
    alpine)
      rc-service docker stop >/dev/null 2>&1 || true
      rc-update del docker default >/dev/null 2>&1 || true
      ;;
    debian)
      systemctl disable --now docker docker.socket containerd >/dev/null 2>&1 || true
      ;;
  esac
  if command -v docker >/dev/null 2>&1; then
    docker rm -f smolvm-instance >/dev/null 2>&1 || true
    docker ps -a --format '{{.Names}}' | grep '^smolvm-' | xargs -r docker rm -f >/dev/null 2>&1 || true
    docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^smolvm|^shelley|^smolvm-shelley' | xargs -r docker image rm -f >/dev/null 2>&1 || true
  fi
  rm -rf /opt/smolvm /opt/smolvm-src /var/lib/smolvm /var/log/smolvm
}

write_config_file() {
  admin_password=${SMOLVM_ADMIN_PASSWORD:-changeme}
  public_host=$(detect_public_host)
  agent_binary_name=$(detect_agent_binary_name)
  outbound_interface=${SMOLVM_OUTBOUND_INTERFACE:-$(detect_outbound_interface)}
  default_openai_api_key=$(resolve_default_openai_key)
  mkdir -p "$SMOLVM_HOME"
  cat > "$SMOLVM_CONFIG_PATH" <<EOF
{
  "listen_addr": "${SMOLVM_LISTEN:-:8090}",
  "data_dir": "${SMOLVM_DATA_DIR}",
  "agent_binary_path": "${SMOLVM_BIN_DIR}/${agent_binary_name}",
  "public_host": "${public_host}",
  "default_openai_api_key": "${default_openai_api_key}",
  "admin_password": "${admin_password}",
  "firecracker_binary_path": "${SMOLVM_BIN_DIR}/firecracker",
  "kernel_image_path": "${SMOLVM_ASSETS_DIR}/vmlinux.bin",
  "alpine_minirootfs_path": "${SMOLVM_ASSETS_DIR}/alpine-minirootfs.tar.gz",
  "bridge_name": "${SMOLVM_BRIDGE_NAME:-smolvm0}",
  "bridge_cidr": "${SMOLVM_BRIDGE_CIDR:-172.22.0.1/16}",
  "bridge_gateway": "${SMOLVM_BRIDGE_GATEWAY:-172.22.0.1}",
  "outbound_interface": "${outbound_interface}"
}
EOF
  chmod 600 "$SMOLVM_CONFIG_PATH"
}

write_openrc_service() {
  cat > /etc/init.d/smolvm <<EOF
#!/sbin/openrc-run
name="smolvm"
description="smolvm admin"
command="${SMOLVM_BIN_DIR}/smolvm-admin"
command_args="--config ${SMOLVM_CONFIG_PATH}"
command_background=true
pidfile="/run/smolvm.pid"
output_log="/var/log/smolvm/current.log"
error_log="/var/log/smolvm/current.log"

depend() {
  after firewall
}

start_pre() {
  checkpath --directory --owner root:root --mode 0755 ${SMOLVM_HOME} ${SMOLVM_DATA_DIR} /var/log/smolvm
}
EOF
  chmod 755 /etc/init.d/smolvm
}

write_systemd_service() {
  cat > /etc/systemd/system/smolvm.service <<EOF
[Unit]
Description=smolvm admin
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${SMOLVM_HOME}
ExecStart=${SMOLVM_BIN_DIR}/smolvm-admin --config ${SMOLVM_CONFIG_PATH}
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
    ;;
  debian)
    install_packages_debian
    ;;
esac

ensure_swap
cleanup_old_docker_artifacts
build_binaries
deploy_binaries
download_firecracker
download_guest_assets

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
