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

install_packages_alpine() {
  say "Installing Alpine prerequisites"
  apk update
  apk add bash build-base ca-certificates coreutils curl e2fsprogs git go openssh qemu-system-x86_64 sqlite tini util-linux
}

install_packages_debian() {
  say "Installing Debian/Ubuntu prerequisites"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends bash build-essential ca-certificates coreutils curl e2fsprogs git golang-go openssh-client qemu-system-x86 sqlite3 tini util-linux
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

detect_qemu_binary() {
  if command -v qemu-system-x86_64 >/dev/null 2>&1; then
    command -v qemu-system-x86_64
    return
  fi
  echo "qemu-system-x86_64 not found" >&2
  exit 1
}

download_guest_assets() {
  say "Downloading Alpine guest assets"
  curl -fsSL -o "$SMOLVM_ASSETS_DIR/alpine-minirootfs.tar.gz" \
    "https://dl-cdn.alpinelinux.org/alpine/v3.21/releases/x86_64/alpine-minirootfs-3.21.2-x86_64.tar.gz"
}

build_guest_template() {
  say "Building Alpine guest template"
  template_image="${SMOLVM_ASSETS_DIR}/alpine-template.ext4"
  template_mount="${SMOLVM_HOME}/.template-mnt"
  agent_binary_name=$(detect_agent_binary_name)
  template_mb=${SMOLVM_TEMPLATE_MB:-1024}

  rm -f "$template_image"
  truncate -s "${template_mb}M" "$template_image"
  printf 'label: dos\nlabel-id: 0xfeedc0de\nunit: sectors\n\n2048,,L,*\n' | sfdisk "$template_image" >/dev/null

  loopdev=$(losetup --find --show --partscan "$template_image")
  mkfs.ext4 -F "${loopdev}p1" >/dev/null

  mkdir -p "$template_mount"
  mount "${loopdev}p1" "$template_mount"
  trap 'umount "$template_mount" >/dev/null 2>&1 || true; losetup -d "$loopdev" >/dev/null 2>&1 || true' EXIT INT TERM

  tar -xzf "$SMOLVM_ASSETS_DIR/alpine-minirootfs.tar.gz" -C "$template_mount"
  mkdir -p \
    "$template_mount/etc/init.d" \
    "$template_mount/etc/runlevels/sysinit" \
    "$template_mount/etc/runlevels/boot" \
    "$template_mount/etc/runlevels/default" \
    "$template_mount/etc/network" \
    "$template_mount/root/.smolvm" \
    "$template_mount/var/lib/smolagent" \
    "$template_mount/workspace" \
    "$template_mount/usr/local/bin" \
    "$template_mount/dev/pts" \
    "$template_mount/dev/shm"

  install -m 755 "$SMOLVM_BIN_DIR/${agent_binary_name}" "$template_mount/usr/local/bin/smolagent"

  cat > "$template_mount/etc/network/interfaces" <<'EOF'
auto lo
iface lo inet loopback

auto eth0
iface eth0 inet static
  address 10.0.2.15
  netmask 255.255.255.0
  gateway 10.0.2.2
EOF

  cat > "$template_mount/etc/inittab" <<'EOF'
::sysinit:/sbin/openrc sysinit
::sysinit:/sbin/openrc boot
::wait:/sbin/openrc default
ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100
::ctrlaltdel:/sbin/reboot
::shutdown:/sbin/openrc shutdown
EOF

  cat > "$template_mount/etc/fstab" <<'EOF'
/dev/vda1   /           ext4    defaults,noatime  0 1
devpts      /dev/pts    devpts  defaults          0 0
proc        /proc       proc    defaults          0 0
sysfs       /sys        sysfs   defaults          0 0
tmpfs       /dev/shm    tmpfs   defaults          0 0
EOF

  cat > "$template_mount/etc/apk/repositories" <<'EOF'
https://dl-cdn.alpinelinux.org/alpine/v3.21/main
https://dl-cdn.alpinelinux.org/alpine/v3.21/community
EOF

  cat > "$template_mount/etc/resolv.conf" <<'EOF'
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF

  cat > "$template_mount/etc/doas.conf" <<'EOF'
permit persist :wheel
permit nopass root
EOF
  chmod 400 "$template_mount/etc/doas.conf"

  cat > "$template_mount/etc/init.d/smolagentd" <<'EOF'
#!/sbin/openrc-run

description="smolagent runtime"
command="/usr/local/bin/smolagent"
command_args="--config /root/.smolvm/smolvm.config.json"
pidfile="/run/smolagent.pid"
command_background="yes"
output_log="/var/log/smolagent.log"
error_log="/var/log/smolagent.log"

depend() {
  after qemunet sshd localmount
}

start_pre() {
  echo "smolagentd: start_pre" > /dev/console
  mkdir -p /run /var/log /root/.smolvm /workspace /var/lib/smolagent
  if [ -f /root/.smolvm/instance.env ]; then
    . /root/.smolvm/instance.env
    export PROJECT_WEB_PORT
  fi
}

start_post() {
  echo "smolagentd: started" > /dev/console
}
EOF
  chmod 755 "$template_mount/etc/init.d/smolagentd"

  cat > "$template_mount/etc/init.d/qemunet" <<'EOF'
#!/sbin/openrc-run

description="Configure guest networking for QEMU user-mode net"

depend() {
  after localmount
  before sshd smolagentd
}

start() {
  ip link set eth0 up || true
  ip addr flush dev eth0 2>/dev/null || true
  ip addr add 10.0.2.15/24 dev eth0
  ip route replace default via 10.0.2.2 dev eth0
  return 0
}
EOF
  chmod 755 "$template_mount/etc/init.d/qemunet"

  mount --bind /dev "$template_mount/dev"
  mount --bind /proc "$template_mount/proc"
  mount --bind /sys "$template_mount/sys"
  mount -t devpts devpts "$template_mount/dev/pts"

chroot "$template_mount" /bin/ash <<'EOF'
apk update >/dev/null
apk add bash ca-certificates doas iproute2 openrc openssh linux-virt >/dev/null
rc-update add devfs sysinit >/dev/null
rc-update add bootmisc boot >/dev/null
rc-update add hostname boot >/dev/null
rc-update add qemunet default >/dev/null
rc-update add sshd default >/dev/null
rc-update add smolagentd default >/dev/null
echo "root:root" | chpasswd
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
ssh-keygen -A >/dev/null 2>&1
EOF
  cp "$template_mount/boot/vmlinuz-virt" "$SMOLVM_ASSETS_DIR/vmlinuz-virt"
  cp "$template_mount/boot/initramfs-virt" "$SMOLVM_ASSETS_DIR/initramfs-virt"

  umount "$template_mount/dev/pts"
  umount "$template_mount/dev"
  umount "$template_mount/proc"
  umount "$template_mount/sys"
  umount "$template_mount"
  losetup -d "$loopdev"
  rmdir "$template_mount" >/dev/null 2>&1 || true
  trap - EXIT INT TERM
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
  default_openai_api_key=$(resolve_default_openai_key)
  qemu_binary=${SMOLVM_QEMU_BINARY:-$(detect_qemu_binary)}
  mkdir -p "$SMOLVM_HOME"
  cat > "$SMOLVM_CONFIG_PATH" <<EOF
{
  "listen_addr": "${SMOLVM_LISTEN:-:8090}",
  "data_dir": "${SMOLVM_DATA_DIR}",
  "agent_binary_path": "${SMOLVM_BIN_DIR}/${agent_binary_name}",
  "public_host": "${public_host}",
  "default_openai_api_key": "${default_openai_api_key}",
  "admin_password": "${admin_password}",
  "qemu_binary_path": "${qemu_binary}",
  "kernel_image_path": "${SMOLVM_ASSETS_DIR}/vmlinuz-virt",
  "initramfs_path": "${SMOLVM_ASSETS_DIR}/initramfs-virt",
  "template_image_path": "${SMOLVM_ASSETS_DIR}/alpine-template.ext4"
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
download_guest_assets
build_guest_template

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
