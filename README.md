# MCP Bridge

A pluggable [Model Context Protocol](https://modelcontextprotocol.io/) server that gives AI assistants access to personal data sources. Currently supports **WhatsApp** (search/read messages, search contacts, send messages) and **Google Docs** (search/read documents with periodic sync), with the architecture ready for additional sources.

Data is stored locally in SQLite and only sent to an LLM when accessed through MCP tools. Each data source registers its own namespaced tools (e.g. `whatsapp_list_chats`, `whatsapp_send_message`).

> *Caution:* as with many MCP servers, this is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). Project injection could lead to private data exfiltration.

## Architecture

A single Go binary (`mcpyeahyouknowme`) with a pluggable `DataSource` interface. Each source owns its tools, storage, and lifecycle.

| Mode | Command | Description |
|------|---------|-------------|
| Core | `mcpyeahyouknowme core` | Core daemon: WhatsApp connection + Google Docs sync, stores data in SQLite, exposes REST API |
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

## Google Docs Setup

Google Docs integration requires OAuth 2.0 credentials:

1. **Get OAuth credentials**

   - Go to [Google Cloud Console](https://console.cloud.google.com/)
   - Create a project or select an existing one
   - Enable the Google Docs API and Google Drive API
   - Create OAuth 2.0 credentials (Desktop App type)
   - Download the client ID and secret

2. **Set environment variables**

   ```bash
   export GOOGLE_CLIENT_ID='your-client-id'
   export GOOGLE_CLIENT_SECRET='your-client-secret'
   ``` (only WhatsApp)
mcpyeahyouknowme whatsapp reset

# Wipe Google Docs token and data (only Google Docs)
mcpyeahyouknowme googledocs
   Add these to your `~/.zshrc` or `~/.bashrc` to persist across sessions.

3. **Authenticate**

   ```bash
   mcpyeahyouknowme googledocs login
   ```

   This opens your browser for OAuth authorization. The token is WhatsApped to `~/.local/share/mcpyeahyouknowme/googledocs_token.json`.

4. **Start the sync daemon**

   The Google Docs sync runs automatically when you start the core daemon:

   ```bash
   mcpyeahyouknowme core
   ```

   Documents are synced every 15 minutes and stored in `googledocs.db`. The MCP server provides three tools: `googledocs_search`, ` googledocs_get_document`, and `googledocs_list_recent`.

5. **Reset Google Docs data** (optional)

   ```bash
   mcpyeahyouknowme googledocs reset
   ```

   This removes the OAuth token and all synced documents.

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GOOGLE_CLIENT_ID` | For Google Docs | OAuth 2.0 client ID from Google Cloud Console |
| `GOOGLE_CLIENT_SECRET` | For Google Docs | OAuth 2.0 client secret |
| `MCP_ENABLE_EMBEDDINGS` | Optional | Set to `false` to disable vector search and skip ONNX Runtime dependency |

## Managing the Daemon

Install the core daemon (runs on login, auto-restarts) and manage it:

```bash
mcpyeahyouknowme install-daemon
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

# Wipe Google Docs token and data (only Google Docs)
mcpyeahyouknowme googledocs reset
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
