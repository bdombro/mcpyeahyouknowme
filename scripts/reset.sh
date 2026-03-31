#!/usr/bin/env bash
# reset.sh - Reset WhatsApp connection and re-login
# ==================================================
#
# Description:
#   Clears WhatsApp authentication state and initiates fresh login.
#   Useful for troubleshooting connection issues or switching accounts.
#
# What it does:
#   1. Deletes stored WhatsApp session/encryption keys
#   2. Shows QR code for re-authentication
#   3. Waits for you to scan with WhatsApp mobile app
#
# Usage:
#   ./scripts/reset.sh    # From repo root
#   just reset            # If using justfile
#
# Prerequisites:
#   - mcpyeahyouknowme binary in PATH (run update.sh or install.sh first)
#   - WhatsApp mobile app (for scanning QR code)
#
# Warning:
#   - Disconnects current WhatsApp session
#   - Does not delete message history (only connection credentials)
#   - Daemon will reconnect automatically after successful login
#
# Notes:
#   - Safe to run if you need to switch WhatsApp accounts
#   - Initial sync may take a few minutes after re-login

set -euo pipefail

mcpyeahyouknowme whatsapp reset
mcpyeahyouknowme whatsapp login
