#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
set -a
source "${ENV_FILE:-.env}"
set +a
export DATA_DIR="${DATA_DIR:-$PWD/data}" FRONTEND_DIR="$PWD/frontend/dist" AI_WORK_DIR="${AI_WORK_DIR:-$PWD/data/ai}" RENDER_SCRIPT="$PWD/bot/render_card.py"
export PYTHON_BIN="${PYTHON_BIN:-$PWD/.venv/bin/python}"
mkdir -p bin
(cd backend && go build -o ../bin/server .)
if [ ! -d frontend/node_modules ]; then (cd frontend && npm ci); fi
(cd frontend && npm run build)
if [ ! -x "$PYTHON_BIN" ]; then python3 -m venv .venv; "$PYTHON_BIN" -m pip install -r bot/requirements.txt; fi
./bin/server &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true' EXIT INT TERM
"$PYTHON_BIN" bot/main.py
