#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SMOLVM_DIR="$SCRIPT_DIR"

say() {
  printf '\n==> %s\n' "$1"
}

fail() {
  echo "$1" >&2
  exit 1
}

detect_home() {
  if [[ -n "${SMOLVM_HOME:-}" ]]; then
    printf '%s\n' "$SMOLVM_HOME"
    return
  fi
  printf '%s/.smolvm\n' "$HOME"
}

detect_public_host() {
  if [[ -n "${SMOLVM_PUBLIC_HOST:-}" ]]; then
    printf '%s\n' "$SMOLVM_PUBLIC_HOST"
    return
  fi
  if [[ "$(uname -s)" == "Darwin" ]]; then
    ipconfig getifaddr en0 2>/dev/null || echo "127.0.0.1"
    return
  fi
  if command -v ip >/dev/null 2>&1; then
    ip -4 route get 1.1.1.1 2>/dev/null | awk '/src/ {for (i = 1; i <= NF; i++) if ($i == "src") {print $(i+1); exit}}'
    return
  fi
  echo "127.0.0.1"
}

resolve_openai_key() {
  if [[ -n "${SMOLVM_DEFAULT_OPENAI_API_KEY:-}" ]]; then
    printf '%s\n' "$SMOLVM_DEFAULT_OPENAI_API_KEY"
    return
  fi
  local key_file="${SMOLVM_KEY_FILE:-$HOME/.openai}"
  if [[ -f "$key_file" ]]; then
    local first_line
    first_line=$(head -n 1 "$key_file" | tr -d '\r\n')
    case "$first_line" in
      OPENAI_API_KEY=*) printf '%s\n' "${first_line#OPENAI_API_KEY=}" ;;
      *) printf '%s\n' "$first_line" ;;
    esac
  fi
}

require_tools() {
  local missing=()
  for tool in curl expect go qemu-img qemu-system-x86_64 ssh-keygen; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      missing+=("$tool")
    fi
  done
  if [[ ${#missing[@]} -gt 0 ]]; then
    printf 'missing required tools: %s\n' "${missing[*]}" >&2
    case "$(uname -s)" in
      Darwin)
        echo "Install them with Homebrew, for example: brew install qemu expect go" >&2
        ;;
      Linux)
        echo "Install them with your package manager before rerunning build.sh." >&2
        ;;
    esac
    exit 1
  fi
}

host_go_builds() {
  say "Building host binaries"
  mkdir -p "$SMOLVM_BIN_DIR" "$SMOLVM_DIR/bin"
  (
    cd "$SMOLVM_DIR"
    go build -buildvcs=false -o "$SMOLVM_BIN_DIR/smolvm-admin" ./
    GOOS=linux GOARCH=amd64 go build -buildvcs=false -o "$SMOLVM_DIR/bin/smolagent-linux-amd64" ./cmd/smolagent
  )
}

ensure_guest_key() {
  mkdir -p "$SMOLVM_KEYS_DIR"
  if [[ ! -f "$GUEST_KEY_PATH" ]]; then
    say "Generating guest admin SSH key"
    ssh-keygen -q -t ed25519 -N "" -f "$GUEST_KEY_PATH"
  fi
}

download_alpine_assets() {
  say "Downloading Alpine installer assets"
  local arch="x86_64"
  local base_url="https://dl-cdn.alpinelinux.org/alpine/v3.21"
  local netboot_url="$base_url/releases/$arch/netboot"
  mkdir -p "$SMOLVM_ASSETS_DIR"
  if [[ ! -f "$SMOLVM_ASSETS_DIR/vmlinuz-lts" ]]; then
    curl -fsSL -o "$SMOLVM_ASSETS_DIR/vmlinuz-lts" "$netboot_url/vmlinuz-lts"
  fi
  if [[ ! -f "$SMOLVM_ASSETS_DIR/initramfs-lts" ]]; then
    curl -fsSL -o "$SMOLVM_ASSETS_DIR/initramfs-lts" "$netboot_url/initramfs-lts"
  fi
  if [[ ! -f "$SMOLVM_ASSETS_DIR/modloop-lts" ]]; then
    curl -fsSL -o "$SMOLVM_ASSETS_DIR/modloop-lts" "$netboot_url/modloop-lts"
  fi
}

qemu_accel() {
  case "$(uname -s)" in
    Linux)
      if [[ "$(uname -m)" == "x86_64" ]]; then
        printf '%s\n' "kvm:tcg"
        return
      fi
      ;;
  esac
  printf '%s\n' "tcg"
}

write_installer_expect() {
  local path="$1"
  cat > "$path" <<'EOF'
#!/usr/bin/expect -f
set timeout -1

set img [lindex $argv 0]
set kernel [lindex $argv 1]
set initrd [lindex $argv 2]
set repo [lindex $argv 3]
set modloop_url [lindex $argv 4]
set rootpass [lindex $argv 5]
set pubkey [lindex $argv 6]
set accel [lindex $argv 7]
set srcdir [lindex $argv 8]
set transcript [lindex $argv 9]

log_file -a $transcript

spawn qemu-system-x86_64 \
  -accel $accel \
  -m 2048 \
  -kernel $kernel \
  -initrd $initrd \
  -append "console=ttyS0 ip=dhcp alpine_repo=$repo modloop=$modloop_url" \
  -drive file=$img,format=qcow2,if=virtio \
  -netdev user,id=net0 \
  -device virtio-net,netdev=net0 \
  -virtfs local,path=$srcdir,mount_tag=hostshare,security_model=none,readonly=on \
  -nographic

expect {
  -re "login:" {
    send "root\r"
  }
}

expect {
  -re "\r\n# " {}
  -re "# " {}
}

send "cat > /root/answers <<'ANSWERS'\r"
send "KEYMAPOPTS=\"us us\"\r"
send "HOSTNAMEOPTS=\"-n smolvm-golden\"\r"
send "INTERFACESOPTS=\"auto lo\r"
send "iface lo inet loopback\r"
send "\r"
send "auto eth0\r"
send "iface eth0 inet dhcp\"\r"
send "DNSOPTS=\"-d local -n 1.1.1.1\"\r"
send "TIMEZONEOPTS=\"-z UTC\"\r"
send "PROXYOPTS=\"none\"\r"
send "APKREPOSOPTS=\"-1\"\r"
send "USEROPTS=\"-u no\"\r"
send "SSHDOPTS=\"openssh\"\r"
send "NTPOPTS=\"none\"\r"
send "DISKOPTS=\"-m sys /dev/vda\"\r"
send "ANSWERS\r"

expect {
  -re "\r\n# " {}
  -re "# " {}
}

send "setup-alpine -e -f /root/answers\r"

set install_complete 0
expect {
  -re "Erase the above disk.*\\(y/n\\)" {
    send "y\r"
    exp_continue
  }
  -re "Installation is complete\\. Please reboot\\." {
    set install_complete 1
    exp_continue
  }
  -re "\r\n# " {
    if {$install_complete} {
      # Installer returned to shell after completing disk install.
    } else {
      exp_continue
    }
  }
  -re "# " {
    if {$install_complete} {
      # Installer returned to shell after completing disk install.
    } else {
      exp_continue
    }
  }
  eof {
    puts stderr "setup-alpine exited before returning to a shell prompt"
    exit 1
  }
}

send "mkdir -p /mnt/target /mnt/hostshare\r"
send "cat > /root/postinstall.sh <<'POSTINSTALL'\r"
send "#!/bin/sh\r"
send "set -eu\r"
send "mount /dev/vda3 /mnt/target || mount /dev/vda2 /mnt/target || mount /dev/vda1 /mnt/target\r"
send "mount --bind /dev /mnt/target/dev\r"
send "mount --bind /proc /mnt/target/proc\r"
send "mount --bind /sys /mnt/target/sys\r"
send "mount -t 9p -o trans=virtio,version=9p2000.L hostshare /mnt/hostshare || { modprobe 9pnet_virtio; mount -t 9p -o trans=virtio,version=9p2000.L hostshare /mnt/hostshare; }\r"
send "mkdir -p /mnt/target/mnt/hostshare\r"
send "mount --bind /mnt/hostshare /mnt/target/mnt/hostshare\r"
send "cat > /mnt/target/root/provision.sh <<'PROVISION'\r"
send "#!/bin/sh\r"
send "set -eu\r"
send "echo 'root:$rootpass' | chpasswd\r"
send "cat >> /etc/apk/repositories <<'EOF'\r"
send "http://dl-cdn.alpinelinux.org/alpine/v3.21/community\r"
send "EOF\r"
send "apk update\r"
send "apk add bash ca-certificates curl doas openssh sqlite\r"
send "mkdir -p /root/.ssh /root/.smolvm /workspace /var/lib/smolagent\r"
send "chmod 700 /root/.ssh\r"
send "cat > /root/.ssh/authorized_keys <<'KEY'\r"
send -- "$pubkey\r"
send "KEY\r"
send "chmod 600 /root/.ssh/authorized_keys\r"
send "install -m 755 /mnt/hostshare/bin/smolagent-linux-amd64 /usr/local/bin/smolagent\r"
send "ssh-keygen -A\r"
send "rc-update add sshd default\r"
send "sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config\r"
send "cat > /root/.smolvm/smolvm.config.json <<'JSON'\r"
send "{\r"
send "  \"listen_addr\": \":9000\",\r"
send "  \"db_path\": \"/var/lib/smolagent/smolagent.db\",\r"
send "  \"workspace_dir\": \"/workspace\",\r"
send "  \"default_model\": \"gpt-5.4\",\r"
send "  \"required_header\": \"X-SmolVM-Admin\"\r"
send "}\r"
send "JSON\r"
send "cat > /root/.smolvm/AGENTS.md <<'AGENTS'\r"
send "Global smolvm guidance:\r"
send "  You are running inside a managed Alpine Linux QEMU virtual machine.\r"
send "  The admin system exposes your project web server directly on the assigned project web port.\r"
send "  The agent UI itself is private behind the admin proxy. Do not ask the user to browse the raw private agent port.\r"
send "  Prefer lightweight dependencies and workflows appropriate for Alpine Linux.\r"
send "  Be mindful of the assigned CPU, RAM, and disk limits.\r"
send "AGENTS\r"
send "cat > /etc/init.d/smolagentd <<'SERVICE'\r"
send "#!/sbin/openrc-run\r"
send "description=\"smolagent runtime\"\r"
send "command=\"/usr/local/bin/smolagent\"\r"
send "command_args=\"--config /root/.smolvm/smolvm.config.json\"\r"
send "pidfile=\"/run/smolagent.pid\"\r"
send "command_background=true\r"
send "output_log=\"/var/log/smolagent.log\"\r"
send "error_log=\"/var/log/smolagent.log\"\r"
send "depend() {\r"
send "  need net\r"
send "}\r"
send "SERVICE\r"
send "chmod +x /etc/init.d/smolagentd\r"
send "rc-update add smolagentd default\r"
send "PROVISION\r"
send "chmod +x /mnt/target/root/provision.sh\r"
send "chroot /mnt/target /bin/sh /root/provision.sh\r"
send "sync\r"
send "umount /mnt/target/mnt/hostshare || true\r"
send "umount /mnt/hostshare || true\r"
send "umount /mnt/target/dev || true\r"
send "umount /mnt/target/proc || true\r"
send "umount /mnt/target/sys || true\r"
send "umount /mnt/target || true\r"
send "echo '__SMOLVM_POSTINSTALL_OK__'\r"
send "poweroff\r"
send "POSTINSTALL\r"
send "chmod +x /root/postinstall.sh\r"
send "sh /root/postinstall.sh\r"
expect {
  -re "__SMOLVM_POSTINSTALL_OK__" {}
  eof {}
}
expect eof
EOF
  chmod +x "$path"
}

build_golden_image() {
  if [[ -f "$GOLDEN_IMAGE_PATH" ]]; then
    say "Existing golden image found"
    echo "Golden image: $GOLDEN_IMAGE_PATH"
    if [[ ! -t 0 ]]; then
      fail "golden image exists and no interactive terminal is available; remove it or rerun interactively"
    fi
    while true; do
      printf 'Use existing golden image? [Y/n]: '
      read -r answer
      case "${answer:-Y}" in
        Y|y|yes|YES)
          say "Reusing existing golden image"
          return
          ;;
        N|n|no|NO)
          say "Rebuilding golden image"
          rm -f "$GOLDEN_IMAGE_PATH"
          break
          ;;
        *)
          echo "Enter y or n." >&2
          ;;
      esac
    done
  fi

  say "Building Alpine golden image"
  mkdir -p "$SMOLVM_TMP_DIR"
  qemu-img create -f qcow2 "$GOLDEN_IMAGE_PATH" "${SMOLVM_GOLDEN_SIZE:-8G}" >/dev/null

  local expect_script="$SMOLVM_TMP_DIR/alpine-install.expect"
  write_installer_expect "$expect_script"

  local base_url="http://dl-cdn.alpinelinux.org/alpine/v3.21"
  local repo="$base_url/main"
  local modloop_url="$base_url/releases/x86_64/netboot/modloop-lts"
  local pubkey
  pubkey=$(tr -d '\r\n' < "${GUEST_KEY_PATH}.pub")

  expect "$expect_script" \
    "$GOLDEN_IMAGE_PATH" \
    "$SMOLVM_ASSETS_DIR/vmlinuz-lts" \
    "$SMOLVM_ASSETS_DIR/initramfs-lts" \
    "$repo" \
    "$modloop_url" \
    "${SMOLVM_GUEST_ROOT_PASSWORD:-root}" \
    "$pubkey" \
    "$(qemu_accel)" \
    "$SMOLVM_DIR" \
    "$SMOLVM_TMP_DIR/alpine-install.log"
}

write_host_config() {
  say "Writing host config"
  local openai_key
  openai_key=$(resolve_openai_key || true)
  cat > "$SMOLVM_CONFIG_PATH" <<EOF
{
  "listen_addr": ":8090",
  "data_dir": "$SMOLVM_DATA_DIR",
  "public_host": "$(detect_public_host)",
  "default_openai_api_key": "${openai_key}",
  "admin_password": "${SMOLVM_ADMIN_PASSWORD:-changeme}",
  "qemu_binary_path": "$(command -v qemu-system-x86_64)",
  "template_image_path": "$GOLDEN_IMAGE_PATH",
  "guest_ssh_key_path": "$GUEST_KEY_PATH"
}
EOF
}

main() {
  [[ -f "$SMOLVM_DIR/go.mod" ]] || fail "expected smolvm sources at $SMOLVM_DIR"

  require_tools

  SMOLVM_HOME=$(detect_home)
  SMOLVM_BIN_DIR="$SMOLVM_HOME/bin"
  SMOLVM_DATA_DIR="${SMOLVM_DATA_DIR:-$SMOLVM_HOME/data}"
  SMOLVM_ASSETS_DIR="$SMOLVM_HOME/assets"
  SMOLVM_KEYS_DIR="$SMOLVM_HOME/keys"
  SMOLVM_TMP_DIR="$SMOLVM_HOME/tmp"
  SMOLVM_CONFIG_PATH="$SMOLVM_HOME/smolvm.config.json"
  GUEST_KEY_PATH="$SMOLVM_KEYS_DIR/guest-admin"
  GOLDEN_IMAGE_PATH="$SMOLVM_ASSETS_DIR/alpine-golden.qcow2"

  mkdir -p "$SMOLVM_BIN_DIR" "$SMOLVM_DATA_DIR" "$SMOLVM_ASSETS_DIR" "$SMOLVM_KEYS_DIR" "$SMOLVM_TMP_DIR"

  host_go_builds
  ensure_guest_key
  download_alpine_assets
  build_golden_image
  write_host_config

  say "Done"
  echo "Host config: $SMOLVM_CONFIG_PATH"
  echo "Golden image: $GOLDEN_IMAGE_PATH"
  echo "Admin binary: $SMOLVM_BIN_DIR/smolvm-admin"
}

main "$@"
