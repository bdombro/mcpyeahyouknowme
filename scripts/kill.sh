#!/usr/bin/env bash
# kill.sh - Force-kill all mcpyeahyouknowme processes
# ====================================================
#
# Description:
#   Emergency stop for all mcpyeahyouknowme processes and cleanup of
#   SQLite lock files. Useful when processes are stuck or unresponsive.
#
# What it does:
#   1. Finds all running mcpyeahyouknowme processes
#   2. Force-kills them with SIGKILL (-9)
#   3. Removes SQLite .db-shm and .db-wal lock files
#
# Usage:
#   ./scripts/kill.sh    # From repo root
#   just kill            # If using justfile
#
# When to use:
#   - Process is frozen/unresponsive
#   - Database lock errors
#   - Before manual debugging or database operations
#   - Testing daemon restart behavior
#
# Notes:
#   - Uses force kill (SIGKILL), so no graceful shutdown
#   - Unloads daemon first to prevent KeepAlive auto-restart during cleanup
#   - Daemon stays unloaded; use `mcpyeahyouknowme start` to restart it
#   - Safe to run; database consistency is maintained
#   - Does not delete any data (only lock files)

set -euo pipefail

DATA_DIR="$HOME/.local/share/mcpyeahyouknowme"

PLIST="$HOME/Library/LaunchAgents/com.mcpyeahyouknowme.core.plist"

# Unload LaunchAgent first to prevent KeepAlive from auto-restarting
if [ -f "$PLIST" ]; then
	echo "Unloading LaunchAgent daemon..."
	launchctl unload "$PLIST" 2>/dev/null || true
	echo "✓ LaunchAgent unloaded"
fi

echo "Killing all mcpyeahyouknowme processes..."

if pgrep -f mcpyeahyouknowme >/dev/null 2>&1; then
	pkill -9 -f mcpyeahyouknowme || true
	echo "✓ Killed all mcpyeahyouknowme processes"
else
	echo "✓ No running processes found"
fi

# Clean up SQLite lock files
if [ -d "$DATA_DIR" ]; then
	rm -f "$DATA_DIR"/*.db-shm "$DATA_DIR"/*.db-wal || true
	echo "✓ Cleaned up database lock files"
fi
