#!/usr/bin/env bash
# lint.sh - Static analysis (go vet + Staticcheck + Revive)
# ========================================================
#
# Description:
#   Runs go vet, then Staticcheck and Revive from the module in src/.
#   Staticcheck and Revive use versions pinned in src/go.mod (go tool).
#   Build tags match scripts/test.sh.
#
# Usage:
#   ./scripts/lint.sh    # From repo root
#
# Prerequisites:
#   - Go 1.26+ (same as the module)
#
# Configuration:
#   - Revive rules: src/revive.toml

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

run_vet() {
	cd "$CLI_DIR"
	go vet -tags=sqlite_fts5 ./...
}

run_staticcheck() {
	cd "$CLI_DIR"
	go tool staticcheck -tags=sqlite_fts5 ./...
}

run_revive() {
	cd "$CLI_DIR"
	GOFLAGS='-tags=sqlite_fts5' go tool revive -config "$CLI_DIR/revive.toml" -set_exit_status ./...
}

main() {
	local status=0
	run_vet || status=1
	run_staticcheck || status=1
	run_revive || status=1
	exit "$status"
}

main "$@"
