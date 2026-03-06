#!/usr/bin/env bash
set -euo pipefail

DB_PATH="${DB_PATH:-./data/reposview.sqlite}"
PORT="${PORT:-8790}"
HOST="${HOST:-127.0.0.1}"
HOME_DIR="${HOME:-/}"
ESCAPED_HOME="$(printf '%s' "$HOME_DIR" | sed 's/[.[\*^$()+?{}|]/\\&/g')"
SCAN_ROOT="${SCAN_ROOT:-$HOME_DIR}"
EXCLUDE_REGEX="${EXCLUDE_REGEX:-^${ESCAPED_HOME}/\\.[^/]+(?:/|$)}"
SCANNER="${SCANNER:-auto}"
API_PORT="${API_PORT:-8787}"
API_HOST="${API_HOST:-127.0.0.1}"

exec pnpm run dev -- \
  --db "$DB_PATH" \
  --host "$HOST" \
  --port "$PORT" \
  --api-host "$API_HOST" \
  --api-port "$API_PORT" \
  --root "$SCAN_ROOT" \
  --exclude-regex "$EXCLUDE_REGEX" \
  --scanner "$SCANNER" \
  "$@"
