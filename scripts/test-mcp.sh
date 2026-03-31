#!/usr/bin/env bash
# test-mcp.sh - MCP protocol smoke test
# =====================================
#
# Description:
#   Quick integration test that verifies the MCP server can:
#   1. Initialize with proper protocol version
#   2. Accept initialized notification
#   3. Execute a search tool call and return results
#
# Usage:
#   ./scripts/test-mcp.sh    # From repo root
#   just test-mcp            # If using justfile
#
# What it tests:
#   - MCP server starts and responds to stdio
#   - Protocol handshake (initialize/initialized)
#   - Search tool execution with query="Eileen"
#
# Prerequisites:
#   - Built binary at mcpyeahyouknowme.bin in repo root (run build.sh first)
#   - WhatsApp data in database (optional, for meaningful results)
#
# Notes:
#   - Runs quickly (~1 second)
#   - Stderr is suppressed (2>/dev/null)
#   - Returns JSON-RPC responses to stdout
#   - Does not require running daemon

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

(
	echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
	echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
	echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"query":"Meeeeee","limit":5}}}'
) | "$ROOT/mcpyeahyouknowme.bin" mcp 2>/dev/null
