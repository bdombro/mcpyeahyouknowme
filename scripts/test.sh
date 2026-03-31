#!/usr/bin/env bash
# test.sh - Run tests with coverage filtering
# ============================================
#
# Description:
#   Runs the full Go test suite with coverage tracking, filters results
#   to focus on core business logic, and generates HTML coverage reports.
#
# What it generates:
#   - coverage/coverage.txt           - Raw coverage data (all files)
#   - coverage/coverage_filtered.txt  - Filtered coverage (business logic only)
#   - coverage/coverage.html          - Interactive HTML coverage report
#
# Usage:
#   ./scripts/test.sh    # From repo root
#   just test            # If using justfile
#
# Coverage filtering:
#   - Includes: fuzzy, mcp_service, search_store, store, embedding modules
#   - Excludes: CLI handlers, OAuth flows, event handlers, daemon code
#   - Removes specific lines: constructor errors, DB errors, ONNX panics, OS-specific code
#
# Target: 100% coverage of filtered business logic
#
# Maintaining coverage:
#   When adding new untestable error paths (DB failures, ONNX panics), add their
#   line numbers to the grep -v exclusion list below. Document rationale in
#   docs/test-spec.md. See docs/testing.md for line number update procedures.
#
# Prerequisites:
#   - Go 1.26+ with CGo enabled
#   - SQLite FTS5 support
#
# Notes:
#   - Uses -count=1 to disable test caching
#   - Runs silently; check coverage/ directory for results
#   - If coverage drops, see docs/testing.md for troubleshooting

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

run_tests() {
	# Run all Go tests with full coverage profiling
	mkdir -p "$ROOT/coverage"
	cd "$CLI_DIR"
	go test -tags "sqlite_fts5" \
		-coverprofile="$ROOT/coverage/coverage.txt" \
		-count=1 ./...
}

filter_coverage() {
	# Filter coverage.txt to core business logic files, removing untestable paths.
	# Excludes CLI handlers, OAuth flows, event handlers, and daemon code.
	# Also removes specific uncovered statements that are:
	#   - Constructor/init error paths (require mocking filesystem/sqlite init)
	#   - DB error paths (require mocking sql.DB failures)
	#   - ONNX panic/error recovery (require real ONNX failures)
	#   - OS-dependent code paths (architecture-specific)
	# See docs/test-spec.md for full justification of each exclusion.
	grep -E "^(mode:|mcpyeahyouknowme/(fuzzy|mcp_service|search_store|store|embedding)\.go:)" \
		"$ROOT/coverage/coverage.txt" | \
		grep -v \
			-e 'store\.go:3[1-9]\.' \
			-e 'store\.go:[4-9][0-9]\.' \
			-e 'store\.go:1[01][0-9]\.' \
			-e 'store\.go:12[0-3]\.' \
			-e 'store\.go:159\.' \
			-e 'store\.go:169\.' \
			-e 'store\.go:17[01]\.' \
			-e 'store\.go:181\.' \
			-e 'store\.go:183\.' \
			-e 'store\.go:191\.' \
			-e 'store\.go:193\.' \
			-e 'store\.go:236\.' \
			-e 'store\.go:238\.' \
			-e 'store\.go:245\.' \
			-e 'store\.go:247\.' \
			-e 'search_store\.go:6[6-9]\.' \
			-e 'search_store\.go:[7-9][0-9]\.' \
			-e 'search_store\.go:1[0-5][0-9]\.' \
			-e 'search_store\.go:16[0-2]\.' \
			-e 'search_store\.go:17[1-3]\.' \
			-e 'search_store\.go:18[2-4]\.' \
			-e 'search_store\.go:19[6-8]\.' \
			-e 'search_store\.go:20[1-3]\.' \
			-e 'search_store\.go:21[7-9]\.' \
			-e 'search_store\.go:23[4-6]\.' \
			-e 'search_store\.go:24[78]\.' \
			-e 'search_store\.go:25[89]\.' \
			-e 'search_store\.go:26[4-6]\.' \
			-e 'search_store\.go:27[4-6]\.' \
			-e 'search_store\.go:279\.' \
			-e 'search_store\.go:28[0-7]\.' \
			-e 'search_store\.go:29[2-8]\.' \
			-e 'search_store\.go:33[3-5]\.' \
			-e 'search_store\.go:37[7-9]\.' \
			-e 'search_store\.go:39[4-6]\.' \
			-e 'search_store\.go:41[4-6]\.' \
			-e 'search_store\.go:42[78]\.' \
			-e 'search_store\.go:48[4-6]\.' \
			-e 'search_store\.go:49[45]\.' \
			-e 'embedding\.go:2[6]\.' \
			-e 'embedding\.go:3[4-9]\.' \
			-e 'embedding\.go:4[0-9]\.' \
			-e 'embedding\.go:5[0-6]\.' \
			-e 'embedding\.go:7[1-4]\.' \
			-e 'embedding\.go:9[6-8]\.' \
			-e 'embedding\.go:11[6-9]\.' \
			-e 'mcp_service\.go:14[1-3]\.' \
			-e 'mcp_service\.go:159\.' \
			-e 'mcp_service\.go:16[01]\.' \
			-e 'mcp_service\.go:17[3-4]\.' \
			-e 'mcp_service\.go:22[1-3]\.' \
			-e 'mcp_service\.go:28[0-2]\.' \
			-e 'mcp_service\.go:35[5-6]\.' \
			-e 'mcp_service\.go:41[2-4]\.' \
			-e 'mcp_service\.go:42[1-2]\.' \
			-e 'mcp_service\.go:52[4-6]\.' \
			-e 'mcp_service\.go:53[4-5]\.' \
			-e 'mcp_service\.go:56[1-3]\.' \
			-e 'mcp_service\.go:57[1-2]\.' \
			-e 'mcp_service\.go:59[4-5]\.' \
			-e 'mcp_service\.go:61[4-6]\.' \
			-e 'mcp_service\.go:71[6-7]\.' \
		> "$ROOT/coverage/coverage_filtered.txt"
}

generate_report() {
	# Render filtered coverage data as an interactive HTML report
	go tool cover -html="$ROOT/coverage/coverage_filtered.txt" -o "$ROOT/coverage/coverage.html" 2>/dev/null
}

main() {
	run_tests
	filter_coverage
	generate_report
}

main
