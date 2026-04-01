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
OUTFILE="$ROOT/mcpyeahyouknowme.bin"

echo "Building binary to $OUTFILE"

rm -rf "$OUTFILE"

# Source .env for Google credentials baked into the binary via ldflags.
if [ -f "$ROOT/.env" ]; then
	set -a
	source "$ROOT/.env"
	set +a
fi

print_source_status() {
	local name="$1"
	local status="$2"
	local detail="${3:-}"
	if [ -n "$detail" ]; then
		echo "  - $name: $status ($detail)"
	else
		echo "  - $name: $status"
	fi
}

echo "Build-time source availability:"
print_source_status "whatsapp" "available"

gsuite_missing=()
[ -z "${GOOGLE_CLIENT_ID:-}" ] && gsuite_missing+=("GOOGLE_CLIENT_ID")
[ -z "${GOOGLE_CLIENT_SECRET:-}" ] && gsuite_missing+=("GOOGLE_CLIENT_SECRET")
if [ ${#gsuite_missing[@]} -eq 0 ]; then
	print_source_status "gsuite" "available"
else
	print_source_status "gsuite" "unavailable" "missing ${gsuite_missing[*]}"
fi

if [ -n "${GOOGLE_PLACE_API_KEY:-}" ]; then
	print_source_status "google_places" "available"
else
	print_source_status "google_places" "unavailable" "missing GOOGLE_PLACE_API_KEY"
fi
echo

build_time="$(date -u '+%Y-%m-%d %H:%M:%S UTC')"
cd "$CLI_DIR" && go build -tags "sqlite_fts5" \
	-ldflags "\
		-X 'main.BuildTime=$build_time' \
		-X 'main.BuildVersion=1.0.0' \
		-X 'mcpyeahyouknowme/sources/gsuite.GoogleClientID=$GOOGLE_CLIENT_ID' \
		-X 'mcpyeahyouknowme/sources/gsuite.GoogleClientSecret=$GOOGLE_CLIENT_SECRET' \
		-X 'mcpyeahyouknowme/sources/google_places.GooglePlaceAPIKey=${GOOGLE_PLACE_API_KEY:-}'" \
	-o "$OUTFILE" .
