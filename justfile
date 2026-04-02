# mcpyeahyouknowme - Project Task Runner
# =====================================
# This justfile is a convenience wrapper around bash scripts in scripts/
#
# All scripts can be run directly: ./scripts/install.sh
# Or via justfile if you have just installed: just install
#
# Usage:
#   just             - Show this help
#   just install     - Full install / update (idempotent)
#   just test           - Cached tests (no coverage)
#   just test-coverage  - Cached tests + coverage
#   just test-full      - No cache + coverage (-count=1)
#   just lint        - go vet + Staticcheck + Revive
#   just uninstall   - Complete uninstall
#
# Prerequisites:
#   - Go 1.26+
#   - Homebrew (for ONNX Runtime, macOS only)
#   - macOS (for daemon features)

# Default recipe shows help
default:
    @just --list

# Build mcpyeahyouknowme into mcpyeahyouknowme.bin at repo root
build:
    @scripts/build.sh

# Bootstrap required Google Cloud APIs and Places key
google-project-setup project_id:
    @scripts/google-project-setup.sh {{project_id}}

# Build, install binary, and restart daemon if binary changed (idempotent)
install:
    @scripts/install.sh

# Run tests (Go test cache on, no coverage)
test:
    @scripts/test.sh

# Run tests with coverage (cache on)
test-coverage:
    @scripts/test.sh --coverage

# Run tests with coverage and disable test cache (-count=1)
test-full:
    @scripts/test.sh --nocache --coverage

# go vet + Staticcheck + Revive (tools pinned in src/go.mod)
lint:
    @scripts/lint.sh

# Smoke-test MCP stdio: initialize, initialized, tools/call search
test-mcp:
    @scripts/test-mcp.sh

# Launch MCP Inspector against `mcpyeahyouknowme mcp`
mcp-browser:
    @scripts/mcp-browser.sh

# mcpyeahyouknowme reset && mcpyeahyouknowme login
reset:
    @scripts/reset.sh

# Kill all mcpyeahyouknowme processes and clean up database locks
kill:
    @scripts/kill.sh

# Kill all processes, remove daemon, data, completions, and binary
uninstall:
    @scripts/uninstall.sh
