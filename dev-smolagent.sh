#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DEV_ROOT="${SMOLAGENT_DEV_ROOT:-$SCRIPT_DIR/.dev/smolagent}"
UI_DIR="${SMOLAGENT_UI_DIR:-$SCRIPT_DIR/agent/ui}"
CONFIG_DIR="$DEV_ROOT/.smolvm"
WORKSPACE_DIR="${SMOLAGENT_WORKSPACE:-$DEV_ROOT/workspace}"
DB_PATH="$DEV_ROOT/smolagent.db"
CONFIG_PATH="$CONFIG_DIR/smolvm.config.json"
LISTEN_ADDR="${SMOLAGENT_LISTEN:-127.0.0.1:9000}"
MODEL="${SMOLAGENT_MODEL:-gpt-5.4}"

mkdir -p "$WORKSPACE_DIR"
mkdir -p "$CONFIG_DIR"

if [ -z "${OPENAI_API_KEY:-}" ]; then
  echo "OPENAI_API_KEY must be set" >&2
  exit 1
fi

cat > "$CONFIG_PATH" <<EOF
{
  "listen_addr": "$LISTEN_ADDR",
  "db_path": "$DB_PATH",
  "workspace_dir": "$WORKSPACE_DIR",
  "ui_dir": "$UI_DIR",
  "default_model": "$MODEL",
  "openai_api_key": "$OPENAI_API_KEY"
}
EOF

exec go run ./cmd/smolagent \
  --config "$CONFIG_PATH"
