#!/usr/bin/env bash
# Starts a local dev server on http://localhost:8080 (dev master key, not for production).
#   ./start.sh        data in ./data
#   ./start.sh demo   demo users and boards in ./data-demo (login admin@demo.local, see log)
set -euo pipefail
cd "$(dirname "$0")"

export MASTER_KEY=dev-only-not-secret
export BASE_URL=http://localhost:8080
export DATA_DIR=./data

if [ "${1:-}" = "demo" ]; then
	export ANDON_DEMO=true
	export DATA_DIR=./data-demo
fi

mkdir -p "$DATA_DIR"
exec go run ./cmd/andon
