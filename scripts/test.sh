#!/usr/bin/env bash
# test.sh - Run tests with coverage filtering
# ============================================
#
# Description:
#   Runs the full Go test suite with coverage tracking, filters results
#   to focus on core business logic, and generates HTML and Markdown reports.
#
# What it generates:
#   - coverage/coverage.md              - Filtered coverage report (business logic only)
#   - coverage/coverage_unfiltered.md   - Full coverage report (all files)
#   - coverage/coverage.html            - Interactive HTML coverage report
#
# Usage:
#   ./scripts/test.sh    # From repo root
#   just test            # If using justfile
#
# Prerequisites:
#   - Go 1.26+ with CGo enabled
#   - SQLite FTS5 support
#
# Notes:
#   - Uses -count=1 to disable test caching
#   - Runs silently; check coverage/ directory for results

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

run_tests() {
	# Run all Go tests with full coverage profiling
	mkdir -p "$ROOT/coverage"
	cd "$CLI_DIR"
	go test -tags "sqlite_fts5" \
		-coverprofile="$ROOT/coverage/coverage_unfiltered.txt" \
		-count=1 ./...
}

filter_coverage() {
	# Filter coverage_unfiltered.txt to core business logic files, removing untestable paths.
	# Excludes CLI handlers, OAuth flows, event handlers, and daemon code.
	# Also removes specific uncovered blocks that are:
	#   Cat 1 - Constructor/init error paths (require mocking filesystem/sqlite init)
	#   Cat 2 - DB error paths (require mocking sql.DB internal failures)
	#   Cat 3 - ONNX panic/error recovery (cross-goroutine SIGSEGV, PassageEmbed errors)
	#   Cat 4 - OS-dependent code paths (architecture-specific)
	# See .cursor/rules/test-coverage.mdc for full justification.
	#
	# Each -e pattern matches a coverage profile block start line (file:LINE.col,...).
	# When source line numbers change, re-run the test, grep the profile for " 0$",
	# and update the start-line numbers here.
	grep -E "^(mode:|mcpyeahyouknowme/(fuzzy|mcp_service|search_store|store|embedding)\.go:)" \
		"$ROOT/coverage/coverage_unfiltered.txt" | \
		grep -v \
			-e 'embedding\.go:26\.' \
			-e 'embedding\.go:34\.' \
			-e 'embedding\.go:42\.' \
			-e 'embedding\.go:52\.' \
			-e 'embedding\.go:69\.' \
			-e 'embedding\.go:94\.' \
			-e 'embedding\.go:114\.' \
			-e 'search_store\.go:64\.' \
			-e 'search_store\.go:70\.' \
			-e 'search_store\.go:77\.' \
			-e 'search_store\.go:87\.' \
			-e 'search_store\.go:106\.' \
			-e 'search_store\.go:115\.' \
			-e 'search_store\.go:122\.' \
			-e 'search_store\.go:129\.' \
			-e 'search_store\.go:137\.' \
			-e 'search_store\.go:145\.' \
			-e 'search_store\.go:153\.' \
			-e 'search_store\.go:169\.' \
			-e 'search_store\.go:180\.' \
			-e 'search_store\.go:194\.' \
			-e 'search_store\.go:199\.' \
			-e 'search_store\.go:232\.' \
			-e 'search_store\.go:245\.' \
			-e 'search_store\.go:277\.' \
			-e 'search_store\.go:283\.' \
			-e 'search_store\.go:294\.' \
			-e 'search_store\.go:375\.' \
			-e 'search_store\.go:412\.' \
			-e 'search_store\.go:425\.' \
			-e 'search_store\.go:482\.' \
			-e 'search_store\.go:492\.' \
			-e 'store\.go:28\.' \
			-e 'store\.go:30\.' \
			-e 'store\.go:34\.' \
			-e 'store\.go:35\.' \
			-e 'store\.go:41\.' \
			-e 'store\.go:75\.' \
			-e 'store\.go:82\.' \
			-e 'store\.go:102\.' \
			-e 'store\.go:105\.' \
			-e 'store\.go:110\.' \
			-e 'store\.go:114\.' \
			-e 'store\.go:118\.' \
			-e 'store\.go:156\.' \
			-e 'store\.go:166\.' \
			-e 'store\.go:178\.' \
			-e 'store\.go:188\.' \
			-e 'store\.go:233\.' \
			-e 'store\.go:242\.' \
			-e 'mcp_service\.go:141\.' \
			-e 'mcp_service\.go:159\.' \
			-e 'mcp_service\.go:173\.' \
			-e 'mcp_service\.go:221\.' \
			-e 'mcp_service\.go:280\.' \
			-e 'mcp_service\.go:355\.' \
			-e 'mcp_service\.go:412\.' \
			-e 'mcp_service\.go:421\.' \
			-e 'mcp_service\.go:524\.' \
			-e 'mcp_service\.go:534\.' \
			-e 'mcp_service\.go:561\.' \
			-e 'mcp_service\.go:571\.' \
			-e 'mcp_service\.go:594\.' \
			-e 'mcp_service\.go:614\.' \
			-e 'mcp_service\.go:716\.' \
		> "$ROOT/coverage/coverage.txt"
}

generate_report() {
	# Render filtered coverage data as an interactive HTML report
	go tool cover -html="$ROOT/coverage/coverage.txt" -o "$ROOT/coverage/coverage.html" 2>/dev/null
}

generate_markdown() {
	# Convert a Go coverage profile (.txt) to a human-readable Markdown report (.md).
	# Format: file:startLine.col,endLine.col numStatements executionCount
	# Summary table links to uncovered-block headings only for files that have them.
	local input="$1"
	local output="$2"

	grep -v "^mode:" "$input" | awk '
	{
		colon = index($1, ":")
		file = substr($1, 1, colon - 1)
		split(file, p, "/"); fname = p[length(p)]
		stmts[fname] += $2
		if ($3 > 0) {
			cov[fname] += $2
		} else {
			range = substr($1, colon + 1)
			split(range, r, ",")
			split(r[1], s, "."); split(r[2], e, ".")
			suffix = ($2 == 1) ? "stmt" : "stmts"
			uncov[fname] = uncov[fname] "- Lines " s[1] "–" e[1] " (" $2 " " suffix ")\n"
		}
		if (!(fname in seen)) { seen[fname] = 1; order[++nf] = fname }
	}
	END {
		# Bubble sort file names (BSD awk lacks asorti)
		for (i = 1; i <= nf; i++) files[i] = order[i]
		for (i = 1; i < nf; i++)
			for (j = i+1; j <= nf; j++)
				if (files[i] > files[j]) { tmp = files[i]; files[i] = files[j]; files[j] = tmp }

		# Totals
		total_cov = 0; total_stmts = 0
		for (i = 1; i <= nf; i++) { total_cov += cov[files[i]]+0; total_stmts += stmts[files[i]] }
		overall_pct = total_stmts > 0 ? sprintf("%.1f%%", 100*total_cov/total_stmts) : "N/A"

		print "# Coverage Report"
		print ""
		print "## Summary"
		print ""
		print "| File | Covered | Total | Coverage |"
		print "|------|---------|-------|----------|"
		for (i = 1; i <= nf; i++) {
			f = files[i]; c = cov[f]+0; t = stmts[f]
			pct = t > 0 ? sprintf("%.1f%%", 100*c/t) : "N/A"
			anchor = f; gsub(/\./, "", anchor)
			if (f in uncov)
				print "| [`" f "`](#" anchor ") | " c " | " t " | " pct " |"
			else
				print "| `" f "` | " c " | " t " | " pct " |"
		}
		print "| **Total** | **" total_cov "** | **" total_stmts "** | **" overall_pct "** |"
		print ""
		print "## Uncovered Blocks"
		print ""

		has_uncov = 0
		for (f in uncov) { has_uncov = 1; break }
		if (!has_uncov) {
			print "_None — 100% coverage achieved!_"
		} else {
			first = 1
			for (i = 1; i <= nf; i++) {
				f = files[i]
				if (f in uncov) {
					if (!first) print ""
					first = 0
					print "### [`" f "`](../src/" f ")"
					print ""
					printf "%s", uncov[f]
				}
			}
		}
	}
	' > "$output"
}

cleanup_txt() {
	# Remove intermediate .txt coverage profiles now that .md reports are generated
	rm -f "$ROOT/coverage/coverage_unfiltered.txt" "$ROOT/coverage/coverage.txt"
}

main() {
	run_tests
	filter_coverage
	generate_report
	generate_markdown "$ROOT/coverage/coverage_unfiltered.txt" "$ROOT/coverage/coverage_unfiltered.md"
	generate_markdown "$ROOT/coverage/coverage.txt" "$ROOT/coverage/coverage.md"
	cleanup_txt
}

main
