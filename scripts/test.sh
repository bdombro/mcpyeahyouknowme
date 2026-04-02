#!/usr/bin/env bash
# test.sh - Run tests with coverage filtering
# ============================================
#
# Description:
#   Runs the Go test suite. --nocache and --coverage are independent.
#
# What it generates (only with --coverage):
#   - coverage/coverage.md              - Filtered coverage report (business logic only)
#   - coverage/coverage_unfiltered.md   - Full coverage report (all files)
#   - coverage/coverage.html            - Interactive HTML coverage report
#
# Usage:
#   ./scripts/test.sh                      # Cached tests
#   ./scripts/test.sh --nocache            # Disable test cache (-count=1)
#   ./scripts/test.sh --coverage           # Cached tests + coverage + reports
#   ./scripts/test.sh --nocache --coverage # Disable cache + coverage + reports
#   ./scripts/test.sh --coverage --require-100
#       # Same as --coverage, but exit 1 if filtered coverage is below 100%
#
# Prerequisites:
#   - Go 1.26+
#
# Notes:
#   - Without --coverage: no -coverprofile, no files under coverage/
#   - --nocache adds -count=1
#   - Runs silently; see coverage/ when using --coverage

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"
NOCACHE=false
COVERAGE=false
REQUIRE_100=false
for arg in "$@"; do
	case "$arg" in
		--nocache) NOCACHE=true ;;
		--coverage) COVERAGE=true ;;
		--require-100) REQUIRE_100=true ;;
		*)
			echo "usage: $0 [--nocache] [--coverage] [--require-100]" >&2
			exit 2
			;;
	esac
done

if $REQUIRE_100 && ! $COVERAGE; then
	echo "error: --require-100 requires --coverage" >&2
	exit 2
fi

run_tests() {
	# Run Go tests. --nocache → -count=1. --coverage → -coverprofile + reports in main.
	if $COVERAGE; then
		mkdir -p "$ROOT/coverage"
	fi
	cd "$CLI_DIR"
	local -a args=()
	if $NOCACHE; then
		args+=(-count=1)
	fi
	if $COVERAGE; then
		args+=(-coverprofile="$ROOT/coverage/coverage_unfiltered.txt")
	fi
	go test "${args[@]}" ./...
}

filter_coverage() {
	# Filter coverage to core business-logic files, excluding:
	#   - *_init.go files (constructors, schema DDL, ONNX wrappers)
	#   - Uncovered blocks containing a "// nocov" source comment
	#   - Uncovered blocks inside functions whose signature has "// nocov"
	#
	# This approach is stable across line-number changes:
	#   *_init.go exclusion uses filename patterns.
	#   // nocov exclusion auto-detects comment locations and function ranges
	#   from source.

	local raw="$ROOT/coverage/coverage_unfiltered.txt"
	local filtered="$ROOT/coverage/coverage.txt"

	# Step 1: Keep only business-logic files, drop *_init.go
	local keep_pat='^(mode:'
	keep_pat+='|mcpyeahyouknowme/(search_store|embedding)\.go:'
	keep_pat+='|mcpyeahyouknowme/sources/whatsapp/(service|store|helpers)\.go:'
	keep_pat+='|mcpyeahyouknowme/sources/gsuite/(source|mcp|client|store|app_.*)\.go:'
	keep_pat+='|mcpyeahyouknowme/sources/google_places/(source|mcp|client)\.go:'
	keep_pat+='|mcpyeahyouknowme/sources/notebook/(source|mcp|store|scanner|extract|vision)\.go:'
	keep_pat+=')'
	grep -E "$keep_pat" "$raw" \
		| grep -v '_init\.go:' \
		> "$filtered.tmp"

	# Step 2: Collect // nocov line numbers and nocov function ranges from source
	# (go/parser via scripts/nocovmeta.go).
	local nocov_meta
	nocov_meta=$(mktemp)
	(cd "$ROOT/scripts" && go run . "$CLI_DIR") > "$nocov_meta"

	# Step 3: Remove uncovered blocks whose line range contains a // nocov comment
	# or sits inside a function whose signature is annotated with // nocov.
	# Coverage profile format: package/file.go:startLine.col,endLine.col stmts count
	# Covered blocks (count > 0) always pass through; only uncovered blocks are checked.
	awk -v nocov_file="$nocov_meta" '
	BEGIN {
		while ((getline line < nocov_file) > 0) {
			split(line, a, "\t")
			if (a[1] == "line") {
				nocov_line[a[2], a[3]+0] = 1
			} else if (a[1] == "func") {
				idx = ++nocov_func_count[a[2]]
				nocov_func_start[a[2], idx] = a[3] + 0
				nocov_func_end[a[2], idx] = a[4] + 0
			}
		}
	}
	/^mode:/ { print; next }
	{
		count = $NF + 0
		if (count > 0) { print; next }

		pos = index($1, ":")
		file_path = substr($1, 1, pos - 1)
		range_part = substr($1, pos + 1)

		split(range_part, r, ",")
		split(r[1], s, ".")
		split(r[2], e, ".")
		start = s[1] + 0
		end_line = e[1] + 0

		skip = 0
		for (i = start; i <= end_line; i++) {
			if ((file_path, i) in nocov_line) { skip = 1; break }
		}
		if (!skip) {
			for (i = 1; i <= nocov_func_count[file_path]; i++) {
				func_start = nocov_func_start[file_path, i]
				func_end = nocov_func_end[file_path, i]
				if (start >= func_start && end_line <= func_end) {
					skip = 1
					break
				}
			}
		}
		if (!skip) print
	}
	' "$filtered.tmp" > "$filtered"

	rm -f "$filtered.tmp" "$nocov_meta"
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

require_filtered_full_coverage() {
	# Exits 1 when the filtered profile has any uncovered statements (count 0).
	local cov_txt="$1"
	perl -lane '
		next if /^mode:/;
		$stmts += $F[1];
		$cov += $F[1] if $F[2] > 0;
		END {
			exit 0 if $stmts == 0;
			exit($cov == $stmts ? 0 : 1);
		}
	' "$cov_txt"
}

main() {
	run_tests
	if ! $COVERAGE; then return; fi

	local raw_txt="$ROOT/coverage/coverage_unfiltered.txt"
	local cov_txt="$ROOT/coverage/coverage.txt"

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

	if $REQUIRE_100; then
		if ! require_filtered_full_coverage "$cov_txt"; then
			echo "error: filtered coverage is not 100% (see $ROOT/coverage/coverage.md)" >&2
			exit 1
		fi
	fi

	cleanup_txt
}

main
