#!/usr/bin/env bash
# mcp-browser.sh - Launch MCP Inspector against mcpyeahyouknowme
# ==============================================================
#
# Description:
#   Starts the Model Context Protocol Inspector and tells it to launch
#   `mcpyeahyouknowme mcp` as the inspected MCP server command.
#
# Usage:
#   ./scripts/mcp-browser.sh    # From repo root
#   just mcp-browser           # If using justfile
#
# Prerequisites:
#   - `npx` available on PATH
#   - `mcpyeahyouknowme` available on PATH
#   - Network access to download `@modelcontextprotocol/inspector` if needed

set -euo pipefail

exec npx @modelcontextprotocol/inspector mcpyeahyouknowme mcp
