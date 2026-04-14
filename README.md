# MCP Yeah You Know Me

**One MCP server. All your personal data.** `mcpyeahyouknowme` connects WhatsApp, Gmail, Docs, Sheets, Calendar, Tasks, Contacts, Slides, local notes, browser history, live web search, and Google Places behind a single [MCP](https://modelcontextprotocol.io/) endpoint — so your AI assistant uses one consistent toolset instead of juggling a patchwork of separate servers.

**Global search across everything.** A single `search` tool performs BM25 keyword search across every connected source at once — emails, chats, calendar events, documents, notes, browser history — ranked by relevance with follow-up metadata pointing to the right source-specific tool for deep reads.

**Offline-first, privacy-preserving.** All data syncs into local SQLite databases. Every read tool queries local files directly — no network latency, nothing leaves your machine until you explicitly ask for it. The background daemon handles sync; the MCP server reads from local caches.

> *Caution:* as with many MCP servers, this is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). Prompt injection could lead to private data exfiltration.

## Architecture

A single Go binary with a pluggable `DataSource` interface. Two long-running modes:

| Mode | Command | Description |
|------|---------|-------------|
| Core daemon | `mcpyeahyouknowme core` | Syncs all sources into local SQLite, maintains the global search index, exposes a REST API for writes |
| MCP server  | `mcpyeahyouknowme mcp`  | Stdio MCP server — reads from local SQLite for every query, proxies writes to the core daemon |

All reads hit local SQLite; the MCP server never calls external APIs directly. Write operations (send message, download media) are proxied through the core daemon's loopback REST API.

See the [product spec](docs/spec.md) for full details.

## Security

Optional controls live in `~/.local/share/mcpyeahyouknowme/config.json` under a top-level **`mcp`** object (safe to omit; defaults apply at runtime without rewriting the file). Any ongoing MCP connections would need to be restarted/loaded to take effect. A **full annotated template** (all sources + `mcp` keys) is in [**config.json reference**](#configjson-reference) below.

| Field | Default | What it does |
|-------|---------|----------------|
| `read_only` | `false` | When `true`, the MCP server registers **no** mutating tools (nothing that changes state or hits write paths). |
| `disabled_tools` | `[]` | Tool names to omit entirely (e.g. `whatsapp_send_message`). |
| `mutating_tools_per_min` | `10` | Per-tool sliding window: each **non–read-only** tool may run at most this many times per minute; further calls return a rate-limit tool error. |
| `whatsapp_send_max_runes` | `1000` | Max Unicode characters allowed in one `whatsapp_send_message` body. Omit or set `≤ 0` to keep the default (`1000`). Set higher if you routinely send long texts via MCP. |

**Audit log:** every MCP tool invocation appends one JSON line to `mcp-audit.log` in the same data directory. Sensitive argument keys (`message`, `query`, `path`, `body`, `base64`, `media_path`) are logged as length-only redactions. The file is trimmed like `core.log` when it grows past 5&nbsp;MiB (newest ~1&nbsp;MiB kept).

**Untrusted content:** results from external or user-controlled sources (e.g. WhatsApp/Gmail message bodies, web search, browser history, notebook reads, and global `search` / `profile_about_me` when hits include those sources) are prefixed with a short security warning and include `_meta` hints where supported.

**`whatsapp_send_message` size cap (complement to rate limits):** not a substitute for `mutating_tools_per_min`, but it bounds how much text one send can carry—useful when an automated agent might otherwise paste huge blobs (accidentally or via prompt injection). Default **1000** Unicode characters per call; override with `mcp.whatsapp_send_max_runes`.

**2FA / OTP redaction (what gets censored):** before persisting to SQLite (and therefore before full-text indexing and MCP reads that come from the local DB), the server runs a **conservative heuristic** on message text to determine if it's a one-time password message and to censor if so.

## Prerequisites

- **macOS** (Apple Silicon or Intel)
- **Homebrew** (for installing dependencies)
- **Go** (to build)

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

   This builds the Go binary, copies it to `/usr/local/bin/mcpyeahyouknowme`, sets up the core daemon, and adds shell completions.

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
mcpyeahyouknowme status

# Refresh the status view every 10 seconds
mcpyeahyouknowme status --live

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

## config.json reference

**Path:** `~/.local/share/mcpyeahyouknowme/config.json` (same folder as the SQLite databases and `mcp-audit.log`).

The daemon normalizes this file over time: every **registered source** gets a `sources.<name>` entry even when disabled, so keys may appear before you configure a source. The **`mcp`** block is optional and is read only by the **`mcpyeahyouknowme mcp`** subprocess.

### Full reference (JSON with comments, tsconfig-style)

**Important:** The Go runtime uses strict **JSON** — **`//` and `/* */` comments are not allowed** in the real file on disk. Treat the block below as documentation: copy the structure and values you need, **delete all comment lines**, then save. (Some editors can strip JSONC to JSON on export.)

```jsonc
{
  // --- sources: one object per integration (names are fixed) ---
  "sources": {
    "brave_search": {
      // Whether this source is turned on in config. Live Brave tools also need
      // BRAVE_API_KEY at build time; there is no API key stored here.
      "enabled": false,
      // When true, the core daemon runs source Reset() on its next loop, then
      // tooling normally clears this flag. You rarely set it by hand.
      // "reset": false,
      // No "auth" blob for brave_search.
    },
    "browser_history": {
      "enabled": false,
      // "auth" selects which Chromium profile DB to snapshot (macOS).
      // Typical values: "chrome", "brave". Use: mcpyeahyouknowme browser_history set <browser>
      "auth": {
        "browser": "chrome"
      }
    },
    "google_places": {
      "enabled": false,
      // Places needs GOOGLE_PLACE_API_KEY at build time; nothing stored under "auth".
    },
    "gsuite": {
      "enabled": false,
      // "auth" only holds per-app toggles. OAuth tokens live in gsuite_token.json
      // next to this file (written by: mcpyeahyouknowme gsuite login).
      "auth": {
        "apps": {
          "docs": false,
          "sheets": false,
          "gmail": false,
          "calendar": false,
          "tasks": false,
          "contacts": false,
          "slides": false
        }
      }
    },
    "notebook": {
      "enabled": false,
      // Local directories to index (markdown, PDF, images). Use:
      // mcpyeahyouknowme notebook add <path>  (populates dirs for you)
      "auth": {
        "dirs": []
      }
    },
    "whatsapp": {
      "enabled": false
      // Pairing/session files live under the data directory; login sets enabled.
      // No "auth" object in config.json for WhatsApp.
    }
  },

  // --- mcp: optional; used only by `mcpyeahyouknowme mcp` (restart MCP to apply) ---
  "mcp": {
    // When true, no mutating tools are registered (no sends, downloads, etc.).
    "read_only": false,

    // Exact tool names to hide entirely, e.g. "whatsapp_send_message".
    "disabled_tools": [],

    // Per mutating tool name: max calls per sliding minute (default 10 if omitted or <= 0).
    "mutating_tools_per_min": 10,

    // Max Unicode runes/words per whatsapp_send_message body (default 1000 if omitted or <= 0).
    "whatsapp_send_max_runes": 1000
  }
}
```