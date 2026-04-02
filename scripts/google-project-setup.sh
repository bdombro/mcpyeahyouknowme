#!/usr/bin/env bash
# google-project-setup.sh - Bootstrap required Google Cloud APIs
# ==============================================================
#
# Description:
#   Validates gcloud access, selects a Google Cloud project, enables the APIs
#   required by the gsuite and google_places sources, creates a restricted
#   Places API key, and prints the remaining manual OAuth setup steps that
#   must be completed in the Google Cloud Console.
#
# Usage:
#   ./scripts/google-project-setup.sh <google-project-id>
#
# Prerequisites:
#   - gcloud CLI installed
#   - Authenticated gcloud session
#   - Permission to configure the target Google Cloud project

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

readonly REQUIRED_APIS=(
  docs.googleapis.com
  drive.googleapis.com
  sheets.googleapis.com
  gmail.googleapis.com
  calendar-json.googleapis.com
  people.googleapis.com
  tasks.googleapis.com
  slides.googleapis.com
  places.googleapis.com
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
8. Copy the Desktop app Client ID and Client Secret.
9. Put them in .env or your shell as GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET.
10. Build/install the app and run: mcpyeahyouknowme gsuite login

Notes:
- A Desktop app client secret is not a meaningful trusted secret in a shipped macOS binary, but Google currently still requires it during token exchange.
- Gmail, Contacts, and other sensitive scopes may require additional Google verification before broad public rollout.
EOF
}

# step_6_create_places_api_key creates or reuses a restricted Places API key.
step_6_create_places_api_key() {
  local project_id="$1"
  local key_id="mcpyeahyouknowme-places"
  local full_key_name="projects/$project_id/locations/global/keys/$key_id"
  local env_file="$ROOT/.env"
  local key_string

  echo "Setting up Places API key..."

  if ! gcloud services api-keys describe "$full_key_name" >/dev/null 2>&1; then
    echo "Creating Places API key (restricted to Places API..."
    gcloud services api-keys create \
      --project="$project_id" \
      --key-id="$key_id" \
      --display-name="mcpyeahyouknowme Places Key" \
      --api-target=service=places.googleapis.com >/dev/null
  else
    echo "Places API key already exists, re-fetching key string."
  fi

  key_string="$(gcloud services api-keys get-key-string "$full_key_name" \
    --format='value(keyString)')"

  if [ -z "$key_string" ]; then
    echo "Error: failed to retrieve Places API key string." >&2
    exit 1
  fi

  if [ ! -f "$env_file" ]; then
    cp "$ROOT/.env.example" "$env_file"
  fi

  if grep -q "^GOOGLE_PLACE_API_KEY=" "$env_file"; then
    sed -i '' "s|^GOOGLE_PLACE_API_KEY=.*|GOOGLE_PLACE_API_KEY=$key_string|" "$env_file"
  else
    echo "GOOGLE_PLACE_API_KEY=$key_string" >> "$env_file"
  fi

  echo "Places API key written to $env_file as GOOGLE_PLACE_API_KEY."
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
  step_6_create_places_api_key "$project_id"
}

main "$@"
