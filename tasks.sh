#!/usr/bin/env bash
# Project tasks (replaces justfile). Run from repository root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$ROOT/src"
DATA_DIR="$HOME/.local/share/mcpyeahyouknowme"

usage() {
	local code="${1:-1}"
	cat <<'EOF'
Usage: ./tasks.sh <command>

Commands:
  build         Build mcpyeahyouknowme (FTS5) into src/mcpyeahyouknowme.bin
  update        build + install binary to /usr/local/bin + restart daemon if running
  install       update + install-onnx + login + install-daemon + zsh completions
  install-onnx  Install ONNX Runtime via Homebrew (for semantic search)
  test          Run tests with coverage summary (fuzzy, mcp packages)
  test-cover    Run tests and open HTML coverage report
  reset         mcpyeahyouknowme reset && mcpyeahyouknowme login
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
	sudo cp "$CLI_DIR/mcpyeahyouknowme.bin" /usr/local/bin/mcpyeahyouknowme
	sudo chmod +x /usr/local/bin/mcpyeahyouknowme
	echo "Installed /usr/local/bin/mcpyeahyouknowme"

	if mcpyeahyouknowme info 2>/dev/null | grep -q "Status:     running"; then
		mcpyeahyouknowme restart
		echo "Restarted core daemon"
	fi
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
	cmd_update
	cmd_install_onnx
	mcpyeahyouknowme install-daemon

	# Note: pipe to /dev/null bc sugarme/tokenizer is noisy
	local comp_line='eval "$(mcpyeahyouknowme completions zsh 2>/dev/null)"'
	if ! grep -qF "$comp_line" ~/.zshrc 2>/dev/null; then
		echo "" >> ~/.zshrc
		echo "$comp_line" >> ~/.zshrc
		echo "Added shell completions to ~/.zshrc (restart your terminal or run: source ~/.zshrc)"
	else
		echo "Shell completions already in ~/.zshrc"
	fi
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
		go tool cover -html=coverage.out
	)
}

cmd_reset() {
	mcpyeahyouknowme whatsapp reset
	mcpyeahyouknowme whatsapp login
}

case "${1:-}" in
	build) cmd_build ;;
	update) cmd_update ;;
	install) cmd_install ;;
	install-onnx) cmd_install_onnx ;;
	test) cmd_test ;;
	test-cover) cmd_test_cover ;;
	reset) cmd_reset ;;
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
