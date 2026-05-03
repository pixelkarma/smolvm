#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DEV_ROOT="${SMOLAGENT_DEV_ROOT:-$SCRIPT_DIR/.dev/smolagent}"
UI_DIR="${SMOLAGENT_UI_DIR:-$SCRIPT_DIR/agent/ui}"
WORKSPACE_DIR="${SMOLAGENT_WORKSPACE:-$DEV_ROOT/workspace}"
DB_PATH="${SMOLAGENT_DB:-$DEV_ROOT/smolagent.db}"
LISTEN_ADDR="${SMOLAGENT_LISTEN:-127.0.0.1:9000}"
MODEL="${SMOLAGENT_MODEL:-gpt-5.4}"

mkdir -p "$WORKSPACE_DIR"

if [ -z "${OPENAI_API_KEY:-}" ]; then
  echo "OPENAI_API_KEY must be set" >&2
  exit 1
fi

exec go run ./cmd/smolagent \
  --listen "$LISTEN_ADDR" \
  --db "$DB_PATH" \
  --workspace "$WORKSPACE_DIR" \
  --ui-dir "$UI_DIR" \
  --model "$MODEL"
