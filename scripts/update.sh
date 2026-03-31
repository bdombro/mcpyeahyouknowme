#!/usr/bin/env bash
# update.sh - Build and install mcpyeahyouknowme binary
# ======================================================
#
# Description:
#   Builds the binary, installs it to ~/.local/bin, updates PATH in .zshrc,
#   and restarts the daemon if currently running.
#
# What it does:
#   1. Calls build.sh to compile the binary
#   2. Creates ~/.local/bin if needed
#   3. Copies binary to ~/.local/bin/mcpyeahyouknowme
#   4. Adds ~/.local/bin to PATH in .zshrc (if not already present)
#   5. Restarts daemon if it's currently running
#
# Usage:
#   ./scripts/update.sh    # From repo root
#   just update            # If using justfile
#
# Prerequisites:
#   - Everything required by build.sh
#   - Write access to ~/.local/bin/
#   - zsh (for PATH configuration)
#
# Notes:
#   - Uses ~/.local/bin instead of /usr/local/bin to avoid macOS security restrictions
#   - Safe to run multiple times (idempotent)
#   - Only modifies .zshrc if PATH entry is missing

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$ROOT/src"

# Build first
"$ROOT/scripts/build.sh"

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
