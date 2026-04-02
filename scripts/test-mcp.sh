#!/usr/bin/env bash
# test-mcp.sh - MCP protocol smoke test
# =====================================
#
# Description:
#   Quick integration test that verifies the MCP server can:
#   1. Initialize with proper protocol version
#   2. Accept initialized notification
#   3. Execute a search tool call and return results
#   4. Execute a Google Places tool call when configured
#
# Usage:
#   ./scripts/test-mcp.sh    # From repo root
#
# What it tests:
#   - MCP server starts and responds to stdio
#   - Protocol handshake (initialize/initialized)
#   - Search tool execution with query="Eileen"
#   - Google Places tool execution with query="Blue Bottle Coffee Oakland"
#
# Prerequisites:
#   - Built binary at mcpyeahyouknowme.bin in repo root (run build.sh first)
#   - WhatsApp data in database (optional, for meaningful results)
#   - jq installed for pretty-printing JSON output
#
# Notes:
#   - Runs quickly (~1 second)
#   - Stderr is suppressed (2>/dev/null)
#   - Pretty-prints JSON with jq
#   - Returns JSON-RPC responses to stdout
#   - Does not require running daemon
#   - The Google Places call succeeds only when the binary was built with GOOGLE_PLACE_API_KEY

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# step_1_run_case sends an MCP request sequence and prints the response.
step_1_run_case() {
	local label="$1"
	local tool_name="$2"
	local arguments="$3"

	echo "$label"
	(
		echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
		echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
		printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s}}\n' "$tool_name" "$arguments"
	) | "$ROOT/mcpyeahyouknowme.bin" mcp 2>/dev/null | jq .
	echo ""
}

main() {
	if ! command -v jq >/dev/null 2>&1; then
		echo "Error: jq is required for ./scripts/test-mcp.sh. Install jq and try again." >&2
		exit 1
	fi

	# step_1_run_case "Search for 'Meeeeee'" "search" '{"query":"Meeeeee","limit":5}'
	# step_1_run_case "Search for 'Missing Cat'" "search" '{"query":"Missing Cat","limit":5}'
	# step_1_run_case "Search for 'Crashed ship' - should match shipwreck" "search" '{"query":"Crashed ship","limit":5}'
	# step_1_run_case "Search for 'malt beverage' - should match beer" "search" '{"query":"malt beverage","limit":5}'
	step_1_run_case "Search for 'squarespace holidays'" "search" '{"query":"squarespace holidays","limit":5}'
	# step_1_run_case "Google Places search for 'Blue Bottle Coffee Oakland'" "google_places_search_places" '{"query":"Blue Bottle Coffee Oakland","max_results":1}'
}

main
