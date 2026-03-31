#!/usr/bin/env bash
# build.sh - Build mcpyeahyouknowme binary with FTS5 support
# ===========================================================
#
# Description:
#   Compiles the Go application with SQLite FTS5 (Full-Text Search) support
#   and embeds build metadata (timestamp and version).
#
# Output:
#   Creates src/mcpyeahyouknowme.bin (~38MB)
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
#   - Output binary is placed in src/ directory

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

build_time="$(date -u '+%Y-%m-%d %H:%M:%S UTC')"
cd "$CLI_DIR" && go build -tags "sqlite_fts5" \
	-ldflags "-X 'main.BuildTime=$build_time' -X 'main.BuildVersion=1.0.0'" \
	-o mcpyeahyouknowme.bin .
