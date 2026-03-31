#!/usr/bin/env bash
# install.sh - Complete mcpyeahyouknowme installation
# =====================================================
#
# Description:
#   Full installation workflow that sets up everything needed to run mcpyeahyouknowme:
#   binary, dependencies, background daemon, and shell completions.
#
# Installation steps:
#   1. Build and install binary (calls update.sh)
#   2. Install ONNX Runtime via Homebrew (for semantic search)
#   3. Install background daemon as macOS LaunchAgent
#   4. Configure zsh shell completions
#
# Usage:
#   ./scripts/install.sh    # From repo root
#   just install            # If using justfile
#
# Prerequisites:
#   - macOS (required for LaunchAgent daemon)
#   - Homebrew (https://brew.sh)
#   - Everything required by update.sh
#
# Post-installation:
#   - Binary installed to: ~/.local/bin/mcpyeahyouknowme
#   - Daemon installed to: ~/Library/LaunchAgents/com.mcpyeahyouknowme.core.plist
#   - Data stored in: ~/.local/share/mcpyeahyouknowme/
#   - Logs available at: ~/.local/share/mcpyeahyouknowme/core.log
#
# Next steps:
#   1. Restart your terminal (to load PATH and completions)
#   2. Run: mcpyeahyouknowme whatsapp login
#   3. Configure MCP server in your AI client
#
# Notes:
#   - Safe to run multiple times (idempotent)
#   - Daemon starts automatically on login and restarts on crash
#   - To uninstall completely, run: ./scripts/uninstall.sh

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="$HOME/.local/share/mcpyeahyouknowme"
PLIST_LABEL="com.mcpyeahyouknowme.core"
INSTALLED_BIN="$HOME/.local/bin/mcpyeahyouknowme"

echo "Starting mcpyeahyouknowme installation..."
echo ""

echo "=== Step 1: Building and updating binary ==="
"$ROOT/scripts/update.sh"
echo "✓ Binary build and installation complete"
echo ""

echo "=== Step 2: Installing ONNX Runtime ==="
if command -v brew >/dev/null 2>&1; then
	if brew list onnxruntime >/dev/null 2>&1; then
		echo "ONNX Runtime already installed via Homebrew"
	else
		echo "Installing ONNX Runtime via Homebrew..."
		brew install onnxruntime
		echo "✓ ONNX Runtime installed"
	fi
else
	echo "Error: Homebrew is required. Install from https://brew.sh" >&2
	exit 1
fi
echo "✓ ONNX Runtime installation complete"
echo ""

echo "=== Step 3: Installing daemon ==="
if [ "$(uname -s)" != "Darwin" ]; then
	echo "Error: install-daemon is only supported on macOS (LaunchAgent)." >&2
	exit 1
fi
if [ ! -x "$INSTALLED_BIN" ]; then
	echo "Error: $INSTALLED_BIN not found or not executable. Run './scripts/update.sh' first." >&2
	exit 1
fi

plist="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"
log_path="$DATA_DIR/core.log"
mkdir -p "$HOME/Library/LaunchAgents"
mkdir -p "$DATA_DIR"

cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${PLIST_LABEL}</string>
	<key>ProgramArguments</key>
	<array>
		<string>${INSTALLED_BIN}</string>
		<string>core</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>${log_path}</string>
	<key>StandardErrorPath</key>
	<string>${log_path}</string>
</dict>
</plist>
EOF

launchctl unload "$plist" 2>/dev/null || true
if ! launchctl load "$plist"; then
	echo "Error: launchctl load failed for $plist" >&2
	exit 1
fi
echo "Installed and started core daemon: $plist"
echo "Logs: $log_path"
echo "✓ Daemon installation complete"
echo ""

echo "=== Step 4: Setting up shell completions ==="
# Note: pipe to /dev/null bc sugarme/tokenizer is noisy
comp_line='eval "$(mcpyeahyouknowme completions zsh 2>/dev/null)"'
if ! grep -qF "$comp_line" ~/.zshrc 2>/dev/null; then
	echo "" >> ~/.zshrc
	echo "$comp_line" >> ~/.zshrc
	echo "✓ Added shell completions to ~/.zshrc (restart your terminal or run: source ~/.zshrc)"
else
	echo "✓ Shell completions already in ~/.zshrc"
fi
echo ""

echo "=== Installation complete! ==="
echo "You can now use: mcpyeahyouknowme --help"
