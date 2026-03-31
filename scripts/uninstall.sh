#!/usr/bin/env bash
# uninstall.sh - Complete removal of mcpyeahyouknowme
# ====================================================
#
# Description:
#   Completely removes mcpyeahyouknowme from your system:
#   processes, daemon, data, shell config, and binary.
#
# What it removes:
#   1. All running mcpyeahyouknowme processes (force-killed)
#   2. Database lock files (.db-shm, .db-wal)
#   3. LaunchAgent daemon plist
#   4. Data directory (~/.local/share/mcpyeahyouknowme/)
#   5. Shell completions from ~/.zshrc
#   6. Binary from ~/.local/bin/ and /usr/local/bin/
#
# Usage:
#   ./scripts/uninstall.sh    # From repo root
#   just uninstall            # If using justfile
#
# What is preserved:
#   - This repository and source code
#   - Any manual backups you created
#
# Warning:
#   - Deletes ALL WhatsApp message data
#   - Deletes ALL Google Docs content data
#   - Cannot be undone
#   - You'll need to re-sync if you reinstall
#
# Prerequisites:
#   - sudo password (only if binary is in /usr/local/bin/)
#
# Notes:
#   - Safe to run multiple times (idempotent)
#   - Makes backup of .zshrc before modifying (.zshrc.bak)
#   - Checks each location before attempting removal
#   - To reinstall: run ./scripts/install.sh

set -euo pipefail

DATA_DIR="$HOME/.local/share/mcpyeahyouknowme"

echo "Starting mcpyeahyouknowme uninstall..."
echo ""

# Step 1: Kill all mcpyeahyouknowme processes
echo "=== Step 1: Killing all mcpyeahyouknowme processes ==="
if pgrep -f mcpyeahyouknowme >/dev/null 2>&1; then
	pkill -9 -f mcpyeahyouknowme || true
	echo "✓ Killed all mcpyeahyouknowme processes"
else
	echo "✓ No running processes found"
fi
echo ""

# Step 2: Clean up SQLite lock files
echo "=== Step 2: Cleaning up database locks ==="
if [ -d "$DATA_DIR" ]; then
	rm -f "$DATA_DIR"/*.db-shm "$DATA_DIR"/*.db-wal || true
	echo "✓ Removed SQLite lock files"
fi
echo ""

# Step 3: Unload and remove daemon
echo "=== Step 3: Removing daemon ==="
plist="$HOME/Library/LaunchAgents/com.mcpyeahyouknowme.core.plist"
if [ -f "$plist" ]; then
	launchctl unload "$plist" 2>/dev/null || true
	rm -f "$plist"
	echo "✓ Removed daemon plist: $plist"
else
	echo "✓ No daemon plist found"
fi
echo ""

# Step 4: Remove data directory
echo "=== Step 4: Removing data directory ==="
if [ -d "$DATA_DIR" ]; then
	rm -rf "$DATA_DIR"
	echo "✓ Removed data directory: $DATA_DIR"
else
	echo "✓ No data directory found"
fi
echo ""

# Step 5: Remove shell completions from .zshrc
echo "=== Step 5: Removing shell completions ==="
if [ -f ~/.zshrc ] && grep -qF "mcpyeahyouknowme completions" ~/.zshrc 2>/dev/null; then
	sed -i.bak '/mcpyeahyouknowme.*completions/d' ~/.zshrc
	echo "✓ Removed shell completions from ~/.zshrc"
else
	echo "✓ No shell completions found in ~/.zshrc"
fi
echo ""

# Step 6: Remove binary
echo "=== Step 6: Removing binary ==="
removed=false
if [ -f "$HOME/.local/bin/mcpyeahyouknowme" ]; then
	rm -f "$HOME/.local/bin/mcpyeahyouknowme"
	echo "✓ Removed $HOME/.local/bin/mcpyeahyouknowme"
	removed=true
fi
# Also clean up old location if it exists
if [ -f /usr/local/bin/mcpyeahyouknowme ]; then
	sudo rm -f /usr/local/bin/mcpyeahyouknowme
	echo "✓ Removed /usr/local/bin/mcpyeahyouknowme"
	removed=true
fi
if [ "$removed" = false ]; then
	echo "✓ Binary not found"
fi
echo ""

echo "=== Uninstall complete! ==="
