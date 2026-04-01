#!/usr/bin/env bash
# build.sh - Build mcpyeahyouknowme binary with FTS5 support
# ===========================================================
#
# Description:
#   Compiles the Go application with SQLite FTS5 (Full-Text Search) support
#   and embeds build metadata (timestamp and version).
#
# Output:
#   Creates mcpyeahyouknowme.bin in repo root (~38MB)
#
# Usage:
#   ./scripts/build.sh    # From repo root
#   just build            # If using justfile
#
# Prerequisites:
#   - Go 1.26+ with CGo enabled
#   - SQLite headers (usually pre-installed on macOS)
#
# Notes:
#   - Uses -tags "sqlite_fts5" for full-text search support
#   - Build time is embedded for version tracking
#   - Output binary is placed in the repo root directory

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

# Source .env for Google OAuth credentials (baked into the binary via ldflags).
if [ -f "$ROOT/.env" ]; then
	set -a
	source "$ROOT/.env"
	set +a
fi

missing=()
[ -z "${GOOGLE_CLIENT_ID:-}" ] && missing+=("GOOGLE_CLIENT_ID")
[ -z "${GOOGLE_CLIENT_SECRET:-}" ] && missing+=("GOOGLE_CLIENT_SECRET")
if [ ${#missing[@]} -gt 0 ]; then
	echo "Error: required variable(s) not set: ${missing[*]}" >&2
	echo "Copy .env.example to .env and fill in the values, or export them in your shell." >&2
	exit 1
fi

build_time="$(date -u '+%Y-%m-%d %H:%M:%S UTC')"
cd "$CLI_DIR" && go build -tags "sqlite_fts5" \
	-ldflags "\
		-X 'main.BuildTime=$build_time' \
		-X 'main.BuildVersion=1.0.0' \
		-X 'mcpyeahyouknowme/sources/googledocs.GoogleClientID=$GOOGLE_CLIENT_ID' \
		-X 'mcpyeahyouknowme/sources/googledocs.GoogleClientSecret=$GOOGLE_CLIENT_SECRET' \
		-X 'mcpyeahyouknowme/sources/googlesheets.GoogleClientID=$GOOGLE_CLIENT_ID' \
		-X 'mcpyeahyouknowme/sources/googlesheets.GoogleClientSecret=$GOOGLE_CLIENT_SECRET'" \
	-o "$ROOT/mcpyeahyouknowme.bin" .
