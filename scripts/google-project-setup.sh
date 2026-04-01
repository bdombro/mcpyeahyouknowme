#!/usr/bin/env bash
# google-project-setup.sh - Bootstrap required Google Cloud APIs
# ==============================================================
#
# Description:
#   Validates gcloud access, selects a Google Cloud project, enables the APIs
#   required by the gsuite source, and prints the remaining manual OAuth setup
#   steps that must be completed in the Google Cloud Console.
#
# Usage:
#   ./scripts/google-project-setup.sh <google-project-id>
#
# Prerequisites:
#   - gcloud CLI installed
#   - Authenticated gcloud session
#   - Permission to configure the target Google Cloud project

set -euo pipefail

readonly REQUIRED_APIS=(
  docs.googleapis.com
  drive.googleapis.com
  sheets.googleapis.com
  gmail.googleapis.com
  calendar-json.googleapis.com
  people.googleapis.com
  tasks.googleapis.com
  slides.googleapis.com
)

usage() {
  echo "Usage: $0 <google-project-id>" >&2
}

# step_1_require_gcloud verifies the gcloud CLI is available.
step_1_require_gcloud() {
  if ! command -v gcloud >/dev/null 2>&1; then
    echo "Error: gcloud is not installed. Install the Google Cloud SDK first." >&2
    exit 1
  fi
}

# step_2_require_auth ensures there is an active gcloud account.
step_2_require_auth() {
  local active_account
  active_account="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null || true)"
  if [ -z "$active_account" ]; then
    echo "Error: no active gcloud account found. Run 'gcloud auth login' first." >&2
    exit 1
  fi
  echo "Using gcloud account: $active_account"
}

# step_3_require_project validates and selects the target project.
step_3_require_project() {
  local project_id="$1"
  if [ -z "$project_id" ]; then
    usage
    exit 1
  fi
  echo "Selecting Google Cloud project: $project_id"
  gcloud config set project "$project_id" >/dev/null
}

# step_4_enable_apis enables the APIs required by the gsuite source.
step_4_enable_apis() {
  echo "Enabling required Google APIs..."
  gcloud services enable "${REQUIRED_APIS[@]}"
}

# step_5_print_manual_steps prints the remaining console-only setup work.
step_5_print_manual_steps() {
  cat <<'EOF'

Next manual steps in Google Cloud Console:

1. Open https://console.cloud.google.com/ and confirm the same project is selected.
2. Open Google Auth Platform.
3. Configure Branding / app information and support email.
4. Choose the Audience:
   - External for public/non-Workspace users
   - Internal only if this app is strictly Workspace-only
5. Add test users while the OAuth app is still unverified.
6. Review Data Access and requested scopes.
7. Create an OAuth client with type "Desktop app".
8. Copy the Desktop app Client ID.
9. Put that Client ID in .env or your shell as GOOGLE_CLIENT_ID.
10. Build/install the app and run: mcpyeahyouknowme gsuite login

Notes:
- A Desktop app client secret is not treated as a trusted secret in a shipped macOS binary.
- Gmail, Contacts, and other sensitive scopes may require additional Google verification before broad public rollout.
EOF
}

main() {
  local project_id="${1:-}"
  if [ "$project_id" = "--help" ] || [ "$project_id" = "-h" ]; then
    usage
    exit 0
  fi
  step_1_require_gcloud
  step_2_require_auth
  step_3_require_project "$project_id"
  step_4_enable_apis
  step_5_print_manual_steps
}

main "$@"
