# mcpyeahyouknowme - Project Task Runner
# =====================================
# This justfile is a convenience wrapper around bash scripts in scripts/
# 
# All scripts can be run directly: ./scripts/install.sh
# Or via justfile if you have just installed: just install
# 
# The actual implementation lives in scripts/*.sh for easy debugging and portability.
# This wrapper provides discoverability and shell completion via just.
#
# Usage:
#   just             - Show this help
#   just install     - Full installation
#   just test        - Run tests
#   just uninstall   - Complete uninstall
#
# Prerequisites:
#   - Go 1.26+ with CGo enabled
#   - Homebrew (for ONNX Runtime, macOS only)
#   - macOS (for daemon features)

# Variables
root := justfile_directory()

# Default recipe shows help
default:
    @just --list

# Build mcpyeahyouknowme (FTS5) into src/mcpyeahyouknowme.bin
build:
    @{{root}}/scripts/build.sh

# build + install binary to ~/.local/bin + add to PATH + restart daemon if running
update:
    @{{root}}/scripts/update.sh

# Full installation: update + install ONNX Runtime + install daemon + shell completions
install:
    @{{root}}/scripts/install.sh

# Run tests and generate coverage reports
test:
    @{{root}}/scripts/test.sh

# Smoke-test MCP stdio: initialize, initialized, tools/call search
test-mcp:
    @{{root}}/scripts/test-mcp.sh

# mcpyeahyouknowme reset && mcpyeahyouknowme login
reset:
    @{{root}}/scripts/reset.sh

# Kill all mcpyeahyouknowme processes and clean up database locks
kill:
    @{{root}}/scripts/kill.sh

# Kill all processes, remove daemon, data, completions, and binary
uninstall:
    @{{root}}/scripts/uninstall.sh
