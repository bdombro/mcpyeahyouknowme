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
	# Filter coverage to core business-logic files, excluding:
	#   - *_init.go files (constructors, schema DDL, ONNX wrappers)
	#   - Uncovered blocks containing a "// nocov" source comment
	#
	# This approach is stable across line-number changes:
	#   *_init.go exclusion uses filename patterns.
	#   // nocov exclusion auto-detects comment locations from source.

	local raw="$ROOT/coverage/coverage_unfiltered.txt"
	local filtered="$ROOT/coverage/coverage.txt"

	# Step 1: Keep only business-logic files, drop *_init.go
	local keep_pat='^(mode:'
	keep_pat+='|mcpyeahyouknowme/(search_store|embedding)\.go:'
	keep_pat+='|mcpyeahyouknowme/sources/whatsapp/(service|store|helpers)\.go:'
	keep_pat+='|mcpyeahyouknowme/sources/gsuite/(source|mcp|client|store|app_.*)\.go:'
	keep_pat+=')'
	grep -E "$keep_pat" "$raw" \
		| grep -v '_init\.go:' \
		> "$filtered.tmp"

	# Step 2: Collect // nocov line numbers from source
	local nocov_lines
	nocov_lines=$(mktemp)
	for src in "$CLI_DIR"/*.go "$CLI_DIR"/sources/whatsapp/*.go "$CLI_DIR"/sources/gsuite/*.go; do
		base=$(basename "$src")
		[[ "$base" == *_test.go ]] && continue
		[[ "$base" == *_init.go ]] && continue
		{ grep -n '// nocov' "$src" 2>/dev/null || true; } | while IFS=: read -r num _; do
			echo "$base $num"
		done
	done > "$nocov_lines"

	# Step 3: Remove uncovered blocks whose line range contains a // nocov comment.
	# Coverage profile format: package/file.go:startLine.col,endLine.col stmts count
	# Covered blocks (count > 0) always pass through; only uncovered blocks are checked.
	awk -v nocov_file="$nocov_lines" '
	BEGIN {
		while ((getline line < nocov_file) > 0) {
			split(line, a, " ")
			nocov[a[1], a[2]+0] = 1
		}
	}
	/^mode:/ { print; next }
	{
		count = $NF + 0
		if (count > 0) { print; next }

		pos = index($1, ":")
		file_path = substr($1, 1, pos - 1)
		range_part = substr($1, pos + 1)

		n = split(file_path, parts, "/")
		filename = parts[n]

		split(range_part, r, ",")
		split(r[1], s, ".")
		split(r[2], e, ".")
		start = s[1] + 0
		end_line = e[1] + 0

		skip = 0
		for (i = start; i <= end_line; i++) {
			if ((filename, i) in nocov) { skip = 1; break }
		}
		if (!skip) print
	}
	' "$filtered.tmp" > "$filtered"

	rm -f "$filtered.tmp" "$nocov_lines"
}

generate_report() {
	# Render filtered coverage data as an interactive HTML report
	go tool cover \
		-html="$ROOT/coverage/coverage.txt" \
		-o    "$ROOT/coverage/coverage.html" \
		2>/dev/null
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
	local raw_txt="$ROOT/coverage/coverage_unfiltered.txt"
	local cov_txt="$ROOT/coverage/coverage.txt"

	run_tests
	filter_coverage
	generate_report
	generate_markdown "$raw_txt" "$ROOT/coverage/coverage_unfiltered.md"
	generate_markdown "$cov_txt" "$ROOT/coverage/coverage.md"

	# Print filtered total to stdout
	perl -lane '
		next if /^mode:/;
		$stmts += $F[1];
		$cov   += $F[1] if $F[2] > 0;
		END { printf "Filtered coverage: %.1f%%\n", $stmts ? 100*$cov/$stmts : 0 }
	' "$cov_txt"

	cleanup_txt
}

main
