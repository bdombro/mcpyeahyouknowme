#!/usr/bin/env bash
# install.sh - Build, install, and configure mcpyeahyouknowme
# ===========================================================
#
# Description:
#   Full installation workflow. Safe to re-run as an update — if the newly
#   built binary is identical to the installed one (md5 match), binary and
#   daemon steps are skipped. Otherwise the daemon is stopped before the
#   binary is replaced, then restarted afterwards.
#
# Installation steps:
#   1. Install ONNX Runtime via Homebrew (skipped if already present)
#   2. Build binary
#   3. (skipped if binary unchanged) Ensure ~/.local/bin is on PATH in .zshrc
#   4. (skipped if binary unchanged) Configure zsh shell completions
#   5. (skipped if binary unchanged) Stop daemon, replace binary
#   6. (skipped if binary unchanged) Start daemon (install plist if not already present)
#
# Usage:
#   ./scripts/install.sh    # From repo root
#   just install            # If using justfile
#
# Prerequisites:
#   - macOS (required for LaunchAgent daemon)
#   - Homebrew (https://brew.sh)
#   - Go 1.26+ with CGo enabled
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
CLI_DIR="$ROOT/src"
BUILT_BIN="$CLI_DIR/mcpyeahyouknowme.bin"
DATA_DIR="$HOME/.local/share/mcpyeahyouknowme"
PLIST_LABEL="com.mcpyeahyouknowme.core"
PLIST="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"
INSTALLED_BIN="$HOME/.local/bin/mcpyeahyouknowme"

step_1_onnx() {
	echo "=== Step 1: Installing ONNX Runtime ==="
	if command -v brew >/dev/null 2>&1; then
		if brew --prefix onnxruntime >/dev/null 2>&1; then
			echo -e "✓ ONNX Runtime already installed\n"
		else
			brew install onnxruntime
			echo -e "✓ ONNX Runtime installed\n"
		fi
	else
		echo "Error: Homebrew is required. Install from https://brew.sh" >&2
		exit 1
	fi
}

step_2_build() {
	echo "=== Step 2: Building binary ==="
	"$ROOT/scripts/build.sh"
	echo -e "✓ Build complete\n"
}

# Returns 0 (true) if the built binary differs from the installed one.
binary_changed() {
	[ ! -f "$INSTALLED_BIN" ] && return 0
	local built_md5 installed_md5
	built_md5=$(md5 -q "$BUILT_BIN")
	installed_md5=$(md5 -q "$INSTALLED_BIN")
	[ "$built_md5" != "$installed_md5" ]
}

step_3_path() {
	echo "=== Step 3: Configuring PATH ==="
	if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
		if [ -f ~/.zshrc ]; then
			if ! grep -qF 'export PATH="$HOME/.local/bin:$PATH"' ~/.zshrc 2>/dev/null; then
				echo "" >> ~/.zshrc
				echo '# Added by mcpyeahyouknowme installer' >> ~/.zshrc
				echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
				echo -e "✓ Added ~/.local/bin to PATH in ~/.zshrc (restart your terminal or run: source ~/.zshrc)\n"
			else
				echo -e "✓ PATH already configured in ~/.zshrc\n"
			fi
		else
			echo "⚠️  ~/.zshrc not found. Add this to your shell config:"
			echo -e "   export PATH=\"\$HOME/.local/bin:\$PATH\"\n"
		fi
	else
		echo -e "✓ PATH already includes ~/.local/bin\n"
	fi
}

step_4_completions() {
	echo "=== Step 4: Configuring shell completions ==="
	local comp_line='eval "$(mcpyeahyouknowme completions zsh 2>/dev/null)"'
	if ! grep -qF "$comp_line" ~/.zshrc 2>/dev/null; then
		echo "" >> ~/.zshrc
		echo "$comp_line" >> ~/.zshrc
		echo -e "✓ Added shell completions to ~/.zshrc (restart your terminal or run: source ~/.zshrc)\n"
	else
		echo -e "✓ Shell completions already in ~/.zshrc\n"
	fi
}

step_5_install_binary() {
	echo "=== Step 5: Installing binary ==="
	if [ -f "$PLIST" ]; then
		echo "Stopping daemon..."
		launchctl unload "$PLIST" 2>/dev/null || true
	fi
	mkdir -p "$HOME/.local/bin"
	cp "$BUILT_BIN" "$INSTALLED_BIN"
	chmod +x "$INSTALLED_BIN"
	# macOS Sequoia+ blocks unsigned binaries with provenance tracking.
	# Clear the provenance xattr and re-sign so Gatekeeper allows execution.
	if xattr -l "$INSTALLED_BIN" 2>/dev/null | grep -q "com.apple.provenance"; then
		if ! xattr -d com.apple.provenance "$INSTALLED_BIN"; then
			echo "⚠️  Failed to remove provenance xattr — binary may be blocked by Gatekeeper" >&2
		fi
	fi
	if ! codesign --force --sign - "$INSTALLED_BIN" 2>&1; then
		echo "⚠️  Code signing failed — binary may be blocked by Gatekeeper" >&2
	fi
	echo -e "✓ Installed $INSTALLED_BIN\n"
}

step_6_daemon() {
	echo "=== Step 6: Starting daemon ==="
	if [ "$(uname -s)" != "Darwin" ]; then
		echo "Error: daemon is only supported on macOS (LaunchAgent)." >&2
		exit 1
	fi
	mkdir -p "$HOME/Library/LaunchAgents"
	mkdir -p "$DATA_DIR"
	local log_path="$DATA_DIR/core.log"

	if [ ! -f "$PLIST" ]; then
		echo "Installing daemon plist..."
		cat >"$PLIST" <<EOF
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
		echo "✓ Daemon plist installed: $PLIST"
		echo "  Logs: $log_path"
	fi

	if ! launchctl load "$PLIST" 2>/dev/null; then
		echo "Error: launchctl load failed for $PLIST" >&2
		exit 1
	fi
	echo -e "✓ Daemon started\n"
}

main() {
	echo "Starting mcpyeahyouknowme installation..."
	echo ""

	step_1_onnx
	step_2_build

	if binary_changed; then
		step_3_path
		step_4_completions
		step_5_install_binary
		step_6_daemon
	else
		echo -e "✓ Binary unchanged — skipping remaining steps\n"
	fi

	echo "=== Installation complete! ==="
	echo "You can now use: mcpyeahyouknowme --help"
}

main
