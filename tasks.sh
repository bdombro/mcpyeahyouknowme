#!/usr/bin/env bash
# Project tasks (replaces justfile). Run from repository root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$ROOT/src"
DATA_DIR="$HOME/.local/share/mcpyeahyouknowme"
ONNX_VERSION="1.17.0"

usage() {
	local code="${1:-1}"
	cat <<'EOF'
Usage: ./tasks.sh <command>

Commands:
  build         Build mcpyeahyouknowme (FTS5) into src/mcpyeahyouknowme.bin
  update        build + install binary to /usr/local/bin + restart daemon if running
  install       update + install-onnx + login + install-daemon + zsh completions
  install-onnx  Download ONNX Runtime to app-local lib (for semantic search)
  test          Run tests with coverage summary (fuzzy, mcp packages)
  test-cover    Run tests and open HTML coverage report
  reset         mcpyeahyouknowme reset && mcpyeahyouknowme login
EOF
	exit "$code"
}

cmd_build() {
	(cd "$CLI_DIR" && go build -tags "sqlite_fts5" -o mcpyeahyouknowme.bin .)
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
	local lib_dir="$DATA_DIR/lib"
	local os_name arch onnx_arch pkg_name
	os_name="$(uname -s)"
	arch="$(uname -m)"

	case "$os_name" in
		Darwin)
			case "$arch" in
				arm64)  onnx_arch="osx-arm64" ;;
				x86_64) onnx_arch="osx-x86_64" ;;
				*)      echo "Unsupported macOS arch: $arch" >&2; return 1 ;;
			esac
			local lib_name="libonnxruntime.dylib"
			;;
		Linux)
			case "$arch" in
				x86_64)  onnx_arch="linux-x64" ;;
				aarch64) onnx_arch="linux-aarch64" ;;
				*)       echo "Unsupported Linux arch: $arch" >&2; return 1 ;;
			esac
			local lib_name="libonnxruntime.so"
			;;
		*)
			echo "Unsupported OS: $os_name" >&2; return 1
			;;
	esac

	if [ -f "$lib_dir/$lib_name" ]; then
		echo "ONNX Runtime already installed at $lib_dir/$lib_name"
		return 0
	fi

	pkg_name="onnxruntime-${onnx_arch}-${ONNX_VERSION}"
	local url="https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/${pkg_name}.tgz"

	echo "Downloading ONNX Runtime v${ONNX_VERSION} for ${onnx_arch}..."
	local tmp_dir
	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT

	curl -fSL "$url" -o "$tmp_dir/onnx.tgz"
	tar -xzf "$tmp_dir/onnx.tgz" -C "$tmp_dir"

	mkdir -p "$lib_dir"
	cp "$tmp_dir/$pkg_name/lib/$lib_name" "$lib_dir/$lib_name"
	echo "Installed ONNX Runtime to $lib_dir/$lib_name"

	trap - EXIT
	rm -rf "$tmp_dir"
}

cmd_install() {
	cmd_update
	cmd_install_onnx
	mcpyeahyouknowme login
	mcpyeahyouknowme install-daemon

	local comp_line='eval "$(mcpyeahyouknowme completions zsh)"'
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
	mcpyeahyouknowme reset
	mcpyeahyouknowme login
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
