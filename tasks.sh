#!/usr/bin/env bash
# Project tasks (replaces justfile). Run from repository root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$ROOT/src"
DATA_DIR="$HOME/.local/share/mcpyeahyouknowme"
PLIST_LABEL="com.mcpyeahyouknowme.core"
INSTALLED_BIN="$HOME/.local/bin/mcpyeahyouknowme"

usage() {
	local code="${1:-1}"
	cat <<'EOF'
Usage: ./tasks.sh <command>

Commands:
  build      Build mcpyeahyouknowme (FTS5) into src/mcpyeahyouknowme.bin
  update     build + install binary to ~/.local/bin + add to PATH + restart daemon if running
  install    update + install-onnx + login + install-daemon + zsh completions
  test       Run tests with coverage summary (fuzzy, mcp packages)
  test-cover Run tests and open HTML coverage report
  test-mcp   Smoke-test MCP stdio: initialize, initialized, tools/call search
  reset      mcpyeahyouknowme reset && mcpyeahyouknowme login
  kill       Kill all mcpyeahyouknowme processes and clean up database locks
  uninstall  Kill all processes, remove daemon, data, completions, and binary
EOF
	exit "$code"
}

cmd_build() {
	local build_time
	build_time="$(date -u '+%Y-%m-%d %H:%M:%S UTC')"
	(cd "$CLI_DIR" && go build -tags "sqlite_fts5" \
		-ldflags "-X 'main.BuildTime=$build_time' -X 'main.BuildVersion=1.0.0'" \
		-o mcpyeahyouknowme.bin .)
}

cmd_update() {
	cmd_build
	
	# Install to ~/.local/bin instead of /usr/local/bin to avoid macOS security restrictions
	mkdir -p "$HOME/.local/bin"
	cp "$CLI_DIR/mcpyeahyouknowme.bin" "$HOME/.local/bin/mcpyeahyouknowme"
	chmod +x "$HOME/.local/bin/mcpyeahyouknowme"
	echo "Installed $HOME/.local/bin/mcpyeahyouknowme"
	
	# Add to PATH in .zshrc if not already there
	if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
		echo ""
		if [ -f ~/.zshrc ]; then
			if ! grep -qF 'export PATH="$HOME/.local/bin:$PATH"' ~/.zshrc 2>/dev/null; then
				echo "" >> ~/.zshrc
				echo '# Added by mcpyeahyouknowme installer' >> ~/.zshrc
				echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
				echo "✓ Added $HOME/.local/bin to PATH in ~/.zshrc"
				echo "  Restart your terminal or run: source ~/.zshrc"
			else
				echo "✓ PATH already configured in ~/.zshrc"
			fi
		else
			echo "⚠️  ~/.zshrc not found. Add this to your shell config:"
			echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
		fi
	fi

	echo "Testing if running daemon needs restart..."
	if "$HOME/.local/bin/mcpyeahyouknowme" info 2>/dev/null | grep -q "Status:     running"; then
		echo "Restarting daemon..."
		"$HOME/.local/bin/mcpyeahyouknowme" restart
		echo "Restarted core daemon"
	fi
}

cmd_install_daemon() {
	if [ "$(uname -s)" != "Darwin" ]; then
		echo "Error: install-daemon is only supported on macOS (LaunchAgent)." >&2
		return 1
	fi
	if [ ! -x "$INSTALLED_BIN" ]; then
		echo "Error: $INSTALLED_BIN not found or not executable. Run ./tasks.sh update first." >&2
		return 1
	fi

	local plist="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"
	local log_path="$DATA_DIR/core.log"
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
		return 1
	fi
	echo "Installed and started core daemon: $plist"
	echo "Logs: $log_path"
}

cmd_install_onnx() {
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
		return 1
	fi
}

cmd_install() {
	echo "Starting mcpyeahyouknowme installation..."
	echo ""
	
	echo "=== Step 1: Building and updating binary ==="
	cmd_update
	echo "✓ Binary build and installation complete"
	echo ""
	
	echo "=== Step 2: Installing ONNX Runtime ==="
	cmd_install_onnx
	echo "✓ ONNX Runtime installation complete"
	echo ""
	
	echo "=== Step 3: Installing daemon ==="
	cmd_install_daemon
	echo "✓ Daemon installation complete"
	echo ""
	
	echo "=== Step 4: Setting up shell completions ==="
	# Note: pipe to /dev/null bc sugarme/tokenizer is noisy
	local comp_line='eval "$(mcpyeahyouknowme completions zsh 2>/dev/null)"'
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
}

cmd_test() {
	(
		cd "$CLI_DIR"
		go test -tags "sqlite_fts5" -coverprofile=coverage.out -count=1 ./...
		echo ""
		echo "=== Coverage summary ==="
		go tool cover -func=coverage.out | grep -E "^(mcpyeahyouknowme/(fuzzy|mcp)|total)"
	)
}

cmd_test_cover() {
	(
		cd "$CLI_DIR"
		go test -tags "sqlite_fts5" -coverprofile=coverage.out -count=1 ./...
		echo ""
		echo "=== Coverage summary ==="
		go tool cover -html=coverage.out
	)
}

cmd_test_mcp() {
	(
		echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
		echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
		echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"query":"Eileen","limit":5}}}'
	) | "$CLI_DIR/mcpyeahyouknowme.bin" mcp 2>/dev/null
}

cmd_reset() {
	mcpyeahyouknowme whatsapp reset
	mcpyeahyouknowme whatsapp login
}

cmd_kill() {
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
}

cmd_uninstall() {
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
	local plist="$HOME/Library/LaunchAgents/com.mcpyeahyouknowme.core.plist"
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
}

case "${1:-}" in
	build) cmd_build ;;
	update) cmd_update ;;
	install) cmd_install ;;
	test) cmd_test ;;
	test-cover) cmd_test_cover ;;
	test-mcp) cmd_test_mcp ;;
	reset) cmd_reset ;;
	kill) cmd_kill ;;
	uninstall) cmd_uninstall ;;
	-h | --help) usage 0 ;;
	"")
		echo "Error: missing command" >&2
		usage 1
		;;
	*)
		echo "Unknown command: $1" >&2
		usage 1
		;;
esac
