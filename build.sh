#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
SHELLEY_DIR="${SHELLEY_SOURCE_DIR:-$REPO_ROOT/shelley}"
SMOLVM_DIR="$SCRIPT_DIR"
SHELLEY_GIT_URL="${SHELLEY_GIT_URL:-https://github.com/boldsoftware/shelley.git}"
SHELLEY_GIT_REF="${SHELLEY_GIT_REF:-}"

if [ "$(id -u)" -ne 0 ]; then
  echo "build.sh must run as root" >&2
  exit 1
fi

if [ ! -f "$SMOLVM_DIR/go.mod" ]; then
  echo "expected smolvm sources at $SMOLVM_DIR" >&2
  exit 1
fi

say() {
  printf '\n==> %s\n' "$1"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

ensure_shelley_source() {
  if [ -f "$SHELLEY_DIR/go.mod" ]; then
    return
  fi
  say "Fetching Shelley source"
  mkdir -p "$(dirname "$SHELLEY_DIR")"
  git clone "$SHELLEY_GIT_URL" "$SHELLEY_DIR"
  if [ -n "$SHELLEY_GIT_REF" ]; then
    git -C "$SHELLEY_DIR" checkout "$SHELLEY_GIT_REF"
  fi
}

ensure_community_repo() {
  if ! grep -q '/community' /etc/apk/repositories; then
    release=$(cat /etc/alpine-release | cut -d. -f1,2)
    echo "https://dl-cdn.alpinelinux.org/alpine/v${release}/community" >> /etc/apk/repositories
  fi
}

detect_public_host() {
  if [ -n "${SMOLVM_PUBLIC_HOST:-}" ]; then
    printf '%s\n' "$SMOLVM_PUBLIC_HOST"
    return
  fi
  ip -4 route get 1.1.1.1 2>/dev/null | awk '/src/ {for (i = 1; i <= NF; i++) if ($i == "src") {print $(i+1); exit}}'
}

detect_shelley_binary_name() {
  arch=$(uname -m)
  case "$arch" in
    aarch64|arm64) printf '%s\n' "shelley-linux-aarch64" ;;
    x86_64|amd64) printf '%s\n' "shelley-linux-x86" ;;
    *)
      echo "unsupported architecture: $arch" >&2
      exit 1
      ;;
  esac
}

build_shelley() {
  arch=$(uname -m)
  case "$arch" in
    aarch64|arm64) target="build-linux-aarch64" ;;
    x86_64|amd64) target="build-linux-x86" ;;
    *)
      echo "unsupported architecture: $arch" >&2
      exit 1
      ;;
  esac
  make -C "$SHELLEY_DIR" "$target"
}

write_openrc_service() {
  cat > /etc/init.d/smolvm <<'EOF'
#!/sbin/openrc-run
name="smolvm"
description="smolvm admin"
command="/opt/smolvm/bin/smolvm-admin"
command_background=true
pidfile="/run/smolvm.pid"
output_log="/var/log/smolvm/current.log"
error_log="/var/log/smolvm/current.log"

depend() {
  need docker
  after firewall
}

start_pre() {
  checkpath --directory --owner root:root --mode 0755 /opt/smolvm/data /var/log/smolvm
  export $(grep -v '^#' /etc/conf.d/smolvm | xargs)
}
EOF
  chmod 755 /etc/init.d/smolvm
}

write_env_file() {
  admin_password=${SMOLVM_ADMIN_PASSWORD:-changeme}
  public_host=$(detect_public_host)
  shelley_binary_name=$(detect_shelley_binary_name)
  cat > /etc/conf.d/smolvm <<EOF
SMOLVM_LISTEN=${SMOLVM_LISTEN:-:8090}
SMOLVM_DATA_DIR=${SMOLVM_DATA_DIR:-/opt/smolvm/data}
SMOLVM_SHELLEY_BINARY=/opt/smolvm/bin/${shelley_binary_name}
SMOLVM_IMAGE=${SMOLVM_IMAGE:-smolvm-shelley:latest}
SMOLVM_PUBLIC_HOST=${public_host}
SMOLVM_SYSTEM_KEY=${SMOLVM_SYSTEM_KEY:-/root/.openai}
SMOLVM_ADMIN_PASSWORD=${admin_password}
EOF
  chmod 644 /etc/conf.d/smolvm
  if [ "$admin_password" = "changeme" ]; then
    echo "warning: using default admin password 'changeme'; change it after first login" >&2
  fi
}

say "Installing Alpine prerequisites"
ensure_community_repo
apk update
apk add \
  bash \
  build-base \
  ca-certificates \
  coreutils \
  curl \
  docker \
  e2fsprogs \
  git \
  go \
  make \
  nodejs \
  npm \
  pnpm \
  sqlite \
  tini \
  util-linux

say "Enabling Docker"
rc-update add docker default >/dev/null 2>&1 || true
rc-service docker start

ensure_shelley_source

say "Building Shelley from source"
build_shelley

say "Building smolvm admin from source"
mkdir -p "$SMOLVM_DIR/bin"
(cd "$SMOLVM_DIR" && go build -o "$SMOLVM_DIR/bin/smolvm-admin" ./)

say "Deploying binaries"
mkdir -p /opt/smolvm/bin /opt/smolvm/data
install -m 755 "$SMOLVM_DIR/bin/smolvm-admin" /opt/smolvm/bin/smolvm-admin
install -m 755 "$SHELLEY_DIR/bin/$(detect_shelley_binary_name)" "/opt/smolvm/bin/$(detect_shelley_binary_name)"

say "Writing service files"
write_env_file
write_openrc_service
rc-update add smolvm default >/dev/null 2>&1 || true

say "Restarting smolvm"
if rc-service smolvm status >/dev/null 2>&1; then
  rc-service smolvm restart
else
  rc-service smolvm start
fi

say "Done"
echo "Admin URL: http://$(detect_public_host):8090/login"
echo "Config: /etc/conf.d/smolvm"
echo "Logs: /var/log/smolvm/current.log"
