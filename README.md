# MCP Bridge

A pluggable [Model Context Protocol](https://modelcontextprotocol.io/) server that gives AI assistants access to personal data sources. Currently supports **WhatsApp** (search/read messages, search contacts, send messages), **Google Suite** (Docs, Sheets, Gmail, Calendar, Tasks, Contacts, Slides), **Google Places** (live business/address lookup), and **Brave Search** (live web search and URL metadata lookup), with the architecture ready for additional sources.

Data is stored locally in SQLite and only sent to an LLM when accessed through MCP tools. Each data source registers its own namespaced tools (e.g. `whatsapp_list_chats`, `whatsapp_send_message`).

> *Caution:* as with many MCP servers, this is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). Project injection could lead to private data exfiltration.

## Architecture

A single Go binary (`mcpyeahyouknowme`) with a pluggable `DataSource` interface. Each source owns its tools, storage, and lifecycle.

| Mode | Command | Description |
|------|---------|-------------|
| Core | `mcpyeahyouknowme core` | Core daemon: WhatsApp connection + Google Suite sync, stores data in SQLite, exposes REST API |
| MCP  | `mcpyeahyouknowme mcp`  | MCP server over stdio. Loads all enabled sources and registers their tools |

Data flows: Claude/Cursor talks MCP (stdio) to `mcpyeahyouknowme mcp`, which loads each data source. Read tools query local SQLite directly; write tools proxy through the source's backend (e.g. WhatsApp core daemon REST API).

See the [product spec](docs/spec.md) for full details.

## Prerequisites

- **macOS** (Apple Silicon or Intel)
- **Homebrew** (for installing dependencies)
- **Go** (to build)
- **FFmpeg** (*optional*) — only needed for automatic audio format conversion when sending voice messages
- **ONNX Runtime** (*optional*, via Homebrew) — required for semantic vector search; `./scripts/install.sh` installs it automatically

## Quick Start

1. **Clone the repository**

   ```bash
   git clone https://github.com/bdombro/mcpyeahyouknowme.git
   cd mcpyeahyouknowme
   ```

2. **Install**

   ```bash
   ./scripts/install.sh
   ```

   This builds the Go binary, copies it to `/usr/local/bin/mcpyeahyouknowme`, installs ONNX Runtime via Homebrew for semantic search, sets up the core daemon, and adds shell completions.

3. **Log in** (first time only)

   ```bash
   mcpyeahyouknowme whatsapp login
   ```

   Scan the QR code with your WhatsApp app. The initial history sync will be captured during login. The daemon will handle reconnection from now on.

4. **Configure the MCP server** in your AI client:

   ```json
   {
     "mcpServers": {
       "whatsapp": {
         "command": "mcpyeahyouknowme",
         "args": ["mcp"]
       }
     }
   }
   ```

   - **Claude Desktop**: `~/Library/Application Support/Claude/claude_desktop_config.json`
   - **Cursor**: `~/.cursor/mcp.json`

5. **Restart Claude Desktop / Cursor**

## Google Suite Setup

Google Suite uses a desktop OAuth client with PKCE. Google still expects both `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` during token exchange, but the binary can still be built without them. If either is missing at build time, the `gsuite` source is marked unavailable until you rebuild with both values.

1. **Bootstrap the Google Cloud project**

   ```bash
   just google-project-setup your-google-project-id
   ```

   This validates `gcloud`, selects the project, enables the required APIs, creates a restricted Places API key, and prints the remaining manual Google Cloud Console steps.

2. **Finish the manual Google Cloud Console steps**

   - Open [Google Cloud Console](https://console.cloud.google.com/) and confirm the project
   - Open **Google Auth Platform**
   - Configure Branding / app information
   - Choose the Audience (`External` for a public app, `Internal` only for Workspace-only use)
   - Add support/contact email and test users while the app is unverified
   - Review the requested scopes
   - Create an OAuth client of type **Desktop app**
   - Copy the Desktop app **Client ID** and **Client Secret**

   A Desktop app client secret is not a meaningful trusted secret in a shipped macOS binary, but Google currently still requires it during token exchange.

3. **Set the client credentials**

   ```bash
   export GOOGLE_CLIENT_ID='your-desktop-client-id'
   export GOOGLE_CLIENT_SECRET='your-desktop-client-secret'
   ```

   Add them to your shell profile or put them in `.env` before building if you want the `gsuite` source enabled in the binary.

4. **Build-time Places API key** (optional)

   `just google-project-setup` will also create a restricted Places API key and write it to `.env` as `GOOGLE_PLACE_API_KEY`. When present during `just build`, it enables the `google_places_*` MCP tools for live business and address lookup.

5. **Build-time Brave Search API key** (optional)

   Set `BRAVE_API_KEY` in `.env` before building to enable the `brave_search_*` MCP tools for live web search and URL metadata lookup. Get a key from the [Brave Search API Dashboard](https://api-dashboard.search.brave.com/).

6. **Authenticate and enable the apps you want**

   ```bash
   mcpyeahyouknowme gsuite login
   ```

   This opens your browser for OAuth authorization, stores the token in `~/.local/share/mcpyeahyouknowme/gsuite_token.json`, stores the account email in `~/.local/share/mcpyeahyouknowme/gsuite_email.txt`, and prompts you to choose which Google apps to enable. Apps start disabled until you explicitly enable them.

7. **Manage enabled apps**

   ```bash
   mcpyeahyouknowme gsuite apps
   ```

8. **Reset Google Suite data** (optional)

   ```bash
   mcpyeahyouknowme gsuite reset
   ```

   This removes the token and synced Google data, then leaves the source disabled until you log in again.

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `BRAVE_API_KEY` | Optional | Build-time Brave Search API key that enables the `brave_search_*` tools |
| `GOOGLE_CLIENT_ID` | Optional | Desktop OAuth client ID from Google Cloud Console; required only if you want the `gsuite` source available |
| `GOOGLE_CLIENT_SECRET` | Optional | Matching desktop OAuth client secret; required only if you want the `gsuite` source available |
| `GOOGLE_PLACE_API_KEY` | Optional | Build-time Places API key that enables the `google_places_*` tools |
| `GOOGLE_PROJECT_ID` | Optional | Used by `scripts/google-project-setup.sh` / `just google-project-setup` |

## Managing the Daemon

Install the core daemon (runs on login, auto-restarts) and manage it:

```bash
mcpyeahyouknowme start
mcpyeahyouknowme stop
mcpyeahyouknowme restart
```

Other commands:

```bash
# Show status and data locations
mcpyeahyouknowme info

# Wipe WhatsApp data and session (only WhatsApp)
mcpyeahyouknowme whatsapp reset

# Wipe Google Suite token and synced data
mcpyeahyouknowme gsuite reset
```

## Uninstalling

To completely remove mcpyeahyouknowme:

```bash
cd /path/to/mcpyeahyouknowme
./scripts/uninstall.sh
```

This will:
- Kill all running mcpyeahyouknowme processes
- Clean up database lock files
- Unload and remove the daemon
- Delete the data directory (`~/.local/share/mcpyeahyouknowme`)
- Remove shell completions from `~/.zshrc`
- Remove the binary from `/usr/local/bin/mcpyeahyouknowme`

## Troubleshooting

- **QR code not displaying**: Restart the CLI. Check that your terminal supports QR rendering.
- **Already logged in**: The CLI reconnects automatically without a QR code.
- **Device limit reached**: Remove an existing device from WhatsApp on your phone (Settings > Linked Devices).
- **No messages loading**: Initial history sync can take several minutes for large accounts. History is only pushed during first pairing. If your database is empty, run `mcpyeahyouknowme whatsapp login --relogin` to re-pair and capture the initial sync.
- **Out of sync**: Run `mcpyeahyouknowme whatsapp reset` to wipe all data, then `mcpyeahyouknowme whatsapp login` to re-authenticate.
- **Session expired / 405 error**: Run `mcpyeahyouknowme whatsapp login --relogin` to clear the stale session and re-pair. The daemon will be restarted automatically.

For additional Claude Desktop troubleshooting, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues).
