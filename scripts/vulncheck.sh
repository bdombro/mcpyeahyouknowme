#!/usr/bin/env bash
# vulncheck.sh - Scan for known vulnerabilities in Go dependencies
# ================================================================
#
# Description:
#   Runs govulncheck against the module in src/ using the Go Vulnerability
#   Database (https://vuln.go.dev). Only reports vulnerabilities reachable
#   via the call graph, minimizing false positives. govulncheck is pinned
#   in src/go.mod (go tool).
#
# Usage:
#   ./scripts/vulncheck.sh    # From repo root
#
# Prerequisites:
#   - Go 1.26+ (same as the module)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

# Runs govulncheck against all packages in the module.
run_vulncheck() {
	cd "$CLI_DIR"
	go tool govulncheck ./...
}

main() {
	run_vulncheck
}

main "$@"
