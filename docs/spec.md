# Product Spec

A single Go binary that provides a pluggable [MCP](https://modelcontextprotocol.io/) server for AI assistants to access personal data sources. Currently supports **WhatsApp** (via [whatsmeow](https://github.com/tulir/whatsmeow)), **Google Docs** (via Google Docs/Drive APIs with OAuth 2.0), and **Google Sheets** (via Google Sheets/Drive APIs with OAuth 2.0), with the architecture designed to support additional sources like Gmail, Google Drive, and others.

## Building

```
./scripts/build.sh
```

The build script sources `.env` from the repository root (if present) and bakes `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` into the binary via `-ldflags` for both Google Docs and Google Sheets. Copy `.env.example` to `.env` and fill in the values before building. If `.env` is missing, the binary still builds but Google features will require the environment variables at runtime.

CGO must be enabled (default on macOS/Linux) since `go-sqlite3` requires it. On Windows, install a C compiler via [MSYS2](https://www.msys2.org/) and run `go env -w CGO_ENABLED=1` first.

## Commands

### WhatsApp Login

```
mcpyeahyouknowme whatsapp login
mcpyeahyouknowme whatsapp login --relogin
```

Authenticates with WhatsApp by displaying a QR code. Scan it with your phone (Settings > Linked Devices). If already logged in, shows account info. Required before running `core` or `install-daemon`.

During first login, the CLI captures WhatsApp's initial history sync and stores it in `messages.db`. This is the only time WhatsApp pushes the full chat history.

The `--relogin` flag clears the existing session and message databases, re-displays the QR code for a fresh pairing, captures the initial history sync, and restarts the core daemon if it was previously running. Use this when the session is stale or when the initial history sync was missed.

### Google Docs Login

```
mcpyeahyouknowme googledocs login
```

Authenticates with Google using OAuth 2.0. Opens the default browser to authorize access to Google Docs and Google Drive APIs. OAuth client credentials are baked into the binary at build time from `.env` via `-ldflags` (see **Building**). The OAuth token is saved to `googledocs_token.json` for subsequent daemon runs.

### Google Sheets Login

```
mcpyeahyouknowme googlesheets login
```

Authenticates with Google using OAuth 2.0. Opens the default browser to authorize access to Google Sheets and Google Drive APIs. Uses the same OAuth client credentials as Google Docs (baked into the binary from `.env`). The OAuth token is saved to `googlesheets_token.json` for subsequent daemon runs. Uses port 8086 for the local OAuth callback (Google Docs uses 8085).

### Core — Data Source Services

```
mcpyeahyouknowme core
```

Runs all available data source core services. For WhatsApp: connects to WhatsApp, listens for messages, syncs history, and starts the REST API server on port 8080. For Google Docs: syncs documents every 5 minutes via the Google Docs/Drive APIs. For Google Sheets: syncs spreadsheets every 5 minutes via the Google Sheets/Drive APIs. Requires authentication for each source.

Re-authentication may be required after ~20 days for WhatsApp. Google Docs OAuth tokens refresh automatically as long as the refresh token is valid.

### MCP — Model Context Protocol Server

```
mcpyeahyouknowme mcp
```

Starts the built-in MCP server over stdio transport. This is what Claude Desktop and Cursor invoke to interact with WhatsApp. It reads directly from the local SQLite databases for queries and proxies write operations (send, download) through the core daemon's REST API at `localhost:8080`. The core daemon must be running for write operations.

Configure in your AI client:

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

For **Claude Desktop**: save to `~/Library/Application Support/Claude/claude_desktop_config.json`
For **Cursor**: save to `~/.cursor/mcp.json`

## Multi-Source Architecture

The MCP server loads data sources via the `DataSource` interface defined in `core/interfaces.go`. Each source lives in its own Go package under `src/sources/<name>/` and registers its own MCP tools namespaced with a prefix (e.g. `whatsapp_`, `googledocs_`).

```go
type DataSource interface {
    Name() string                          // prefix for tool names (e.g. "whatsapp")
    Description() string                   // human label (e.g. "WhatsApp")
    RegisterTools(s *server.MCPServer)     // register all tools
    SearchEntries() ([]SearchEntry, error) // provide indexable content for global search
    Reset(dataDir string) error            // remove all source data files
    Close() error                          // release resources
}
```

Each source also implements `CoreService` (in `daemon.go`) for the background sync daemon:

```go
type CoreService interface {
    StartCore(ctx context.Context) error
    RequiresAuth() bool
}
```

### Package structure

```
src/
  core/          — shared interfaces, helpers (DataDir, IntArg, BoolArg, JsonResult),
                   utilities (DefaultReset, RunPollLoop, OpenDB), config (LoadConfig, SaveConfig)
  sources/
    whatsapp/    — store, service, mcp, daemon, client, cli, helpers
    googledocs/  — source, mcp, daemon, client, cli
  config.go      — delegates to core.LoadConfig / core.SaveConfig + legacy migration
  main.go        — CLI dispatch + runCore() polling loop
  mcp.go         — MCP server setup + global search tool
  search_store.go — cross-source search index
```

### Config-driven daemon

The daemon (`runCore()`) reads `{DataDir}/config.json` every 10 seconds and:
- Starts newly-enabled sources
- Stops removed/disabled sources
- Handles `reset: true` by calling `source.Reset()` then removing the config entry

Login commands (`whatsapp login`, `googledocs login`) write `{enabled: true}` to config on success so the daemon picks them up within 10 seconds without a restart.

On first run after upgrade from a pre-config version, the daemon auto-migrates existing auth artifacts (`whatsapp.db` session, `googledocs_token.json`) into config.json.

`LoadSources()` in `main.go` returns all sources for MCP read access. To add a new source:

1. Create `src/sources/<name>/` implementing `core.DataSource` and `core.CoreService`
2. Add it to `LoadSources()` in `main.go`
3. Add it to `constructSource()` in `main.go`

Current sources:

| Source | Prefix | Package | Description |
|--------|--------|---------|-------------|
| WhatsApp | `whatsapp_` | `sources/whatsapp/` | Messages, chats, contacts via local SQLite + REST API |
| Google Docs | `googledocs_` | `sources/googledocs/` | Documents via Google Docs/Drive APIs with 5-minute polling sync |

### Daemon Management

```
mcpyeahyouknowme start
mcpyeahyouknowme stop
mcpyeahyouknowme restart
```

| Command | Description |
|---------|-------------|
| `install-daemon` | Installs and starts the core daemon as a macOS LaunchAgent (`com.mcpyeahyouknowme.core`). Runs on login and auto-restarts on crash. Logs to `~/.local/share/mcpyeahyouknowme/core.log`. |
| `start` | Starts the core daemon. |
| `stop` | Stops the core daemon. |
| `restart` | Restarts the core daemon (stop + start). |

### Maintenance

```
mcpyeahyouknowme info
mcpyeahyouknowme whatsapp reset
mcpyeahyouknowme googledocs reset
```

| Command | Description |
|---------|-------------|
| `info` | Shows build metadata; global data directory status; per-source sections (WhatsApp session and message counts, Google Docs login email and synced document count); and core daemon install status. |
| `whatsapp reset` | Writes `reset: true` to config.json. If the daemon is running, it stops WhatsApp, removes `whatsapp.db` and `messages.db`, then continues running other sources. If the daemon is not running, resets directly. |
| `googledocs reset` | Prompts for confirmation, then writes `reset: true` to config.json. If the daemon is running, it stops Google Docs, removes `googledocs_token.json`, `googledocs_email.txt`, and `googledocs.db`, then continues running other sources. If the daemon is not running, resets directly. |

**Uninstalling:** For complete removal of the application, use `./scripts/uninstall.sh` from the repository root. This kills all processes, removes the daemon, wipes all data, removes shell completions, and deletes the binary from `/usr/local/bin`. See the [README](../README.md) for details.

### Shell Completions

```
mcpyeahyouknowme completions bash
mcpyeahyouknowme completions zsh
```

Add to your shell profile:

```bash
eval "$(mcpyeahyouknowme completions zsh)"
```

---

## WhatsApp Connection

- Connects via [whatsmeow](https://github.com/tulir/whatsmeow) as a linked companion device
- Handles QR code pairing flow (3-minute timeout)
- Automatically reconnects on subsequent runs using session stored in `whatsapp.db`
- Listens for real-time message events and history sync events

### How whatsmeow Works

whatsmeow is an unofficial Go library that implements the WhatsApp Web multidevice protocol. It connects as a "linked device" — the same mechanism WhatsApp Web and WhatsApp Desktop use. Key concepts:

- **Session store** (`whatsapp.db`) — whatsmeow persists device credentials, encryption keys, contact data, and LID (Linked Identity) mappings in a SQLite database. The CLI uses whatsmeow's built-in `sqlstore` driver.
- **Event-driven** — the CLI registers event handlers on whatsmeow's `Client`. Incoming messages arrive as `events.Message`, history sync batches arrive as `events.HistorySync`, and connection status changes arrive as `events.Connected`, `events.Disconnected`, etc.
- **Protobuf wire format** — WhatsApp messages are defined as Protocol Buffer messages. whatsmeow decodes them into Go structs (e.g. `waProto.Message`, `waProto.WebMessageInfo`, `waProto.Conversation`). The CLI extracts text content, media metadata, sender info, and group participant lists from these protobufs.
- **Contact and LID databases** — whatsmeow automatically processes push names and phone-to-LID mappings that arrive in history sync payloads and stores them in `whatsmeow_contacts` and `whatsmeow_lid_map` tables within `whatsapp.db`. The CLI reads these tables for name resolution but never writes to them directly.

### History Sync

When WhatsApp pushes historical conversations (on first connect or periodically), the CLI processes each conversation to maximise the amount of data captured:

1. Resolves the chat name (group name or contact name)
2. Extracts group participants directly from conversation metadata (the `Participant` field on the `Conversation` proto) and stores them in the `group_participants` table. This provides participant data even for groups the user has since left.
3. Extracts message content and media metadata. Non-text message types (stickers, contacts, locations, polls, reactions, etc.) are stored with descriptive placeholder text instead of being silently dropped. Supported content types:
   - Plain text and extended text are stored verbatim
   - Media (image, video, audio, document) stores metadata (type, filename, URL, encryption keys)
   - Stickers, contacts, locations, group invites, polls, reactions, lists, buttons, view-once, and ephemeral messages are all captured with descriptive text
4. Determines the message sender using multiple fallback fields: `Key.Participant`, `WebMessageInfo.Participant`, `PushName`, and finally the chat JID. WhatsApp populates these fields inconsistently, so checking all of them maximises sender attribution.
5. Stores each message with sender, timestamp, and media info
6. Requests up to 500 messages per on-demand history sync via `SendPeerMessage`

Push names (display names) and phone-to-LID mappings included in the history sync payload are processed automatically by whatsmeow and stored in the contacts database for later name resolution.

### WhatsApp API Limitations

WhatsApp's servers and the multidevice protocol have several known limitations that affect data completeness:

**Group names** — The `GetJoinedGroups` and `GetGroupInfo` APIs return participant lists for most groups but omit the group name (`Subject` field) for a significant fraction (~40%) of groups. This appears to be a server-side limitation that varies by group type, creation date, or privacy settings.

**Group sender attribution** — History sync messages in group chats frequently omit the individual sender. The `Key.Participant` field (which should identify who sent the message) is often nil. A separate `WebMessageInfo.Participant` field and the `PushName` field sometimes carry this data, but many group messages arrive with no sender attribution at all.

**Groups the user has left** — `GetJoinedGroups` only returns currently joined groups. For groups the user has since left, individual `GetGroupInfo` calls may fail with 401/404 errors if the group no longer allows access. The CLI mitigates this by extracting participants from history sync conversation metadata, which is available regardless of current group membership.

**History sync completeness** — WhatsApp controls how much history it pushes to linked devices. The initial sync typically delivers recent messages (days to weeks), not full history. The CLI requests up to 500 messages per on-demand sync but the server may deliver fewer. There is no API to request messages older than what the server chooses to provide.

**Linked IDs (LIDs)** — WhatsApp uses opaque Linked IDs (`number@lid`) internally. The `whatsmeow_lid_map` table maps LIDs to phone numbers, but this mapping is only populated for contacts encountered during the session. Some LIDs may never resolve to a phone number if the contact was never seen in a push name or history sync event.

**Contact names** — The `whatsmeow_contacts` table stores names as either `full_name` (from the user's address book, synced from the phone) or `push_name` (the name the contact has set for themselves). Address book names are only available if the phone syncs contacts to WhatsApp. Push names may change and only the most recent value is stored.

### Media Handling

**Incoming media** — metadata (type, filename, URL, encryption keys, SHA256 hashes, file length) is extracted and stored alongside the message. Supported types: image (jpg, png, gif, webp), video (mp4, avi, mov), audio (ogg/opus), and documents.

**Sending media** — the CLI reads the local file, determines MIME type from extension, uploads to WhatsApp servers, and sends the appropriate message type (ImageMessage, AudioMessage, VideoMessage, DocumentMessage). Audio files in ogg/opus format are sent as voice messages with duration and waveform metadata.

**Downloading media** — reconstructs download parameters from stored metadata, downloads via whatsmeow, and saves to `~/.local/share/mcpyeahyouknowme/{chat_jid}/`. Returns the absolute file path. Files are cached so repeated downloads are a no-op.

---

## REST API

The core daemon starts an HTTP server on port **8080** with two endpoints:

### POST /api/send

Send a text message or media file to a recipient.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `recipient` | string | Yes | Phone number or full JID (e.g. `number@s.whatsapp.net`, `number@g.us`) |
| `message` | string | * | Text content or caption for media |
| `media_path` | string | * | Local file path to send as media |

\* At least one of `message` or `media_path` must be provided.

**Response:** `{ "success": bool, "message": string }`

### POST /api/download

Download media from a previously received message.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `message_id` | string | Yes | Message ID |
| `chat_jid` | string | Yes | Chat JID the message belongs to |

**Response:** `{ "success": bool, "message": string, "filename": string, "file_path": string, "media_type": string }`

Note: Request body uses JSON with `message_id` and `chat_jid` fields (not query parameters).

---

## MCP Tools

The MCP server uses [mcp-go](https://github.com/mark3labs/mcp-go) as the framework. Communication is over stdio (JSON-RPC 2.0). Source-specific tool names are prefixed with their source name (e.g. `whatsapp_`). The global `search` tool is not prefixed.

### MCP tool RPC methods

These are the JSON-RPC methods the server exposes for discovering and invoking tools (after `initialize` and `notifications/initialized`):

| Method | Role |
|--------|------|
| `tools/list` | List tool names, schemas, descriptions |
| `tools/call` | Run a tool (e.g. `search`, `whatsapp_*`, …) |

Use `tools/call` with `params.name` set to one of the tool names below and `params.arguments` as a JSON object of that tool’s parameters (see the following sections for full parameter lists and behavior).

### `tools/call` tool names

| Tool name | Description |
|-----------|-------------|
| `search` | Global hybrid search across connected sources (BM25 + optional vectors); optional `source`, `content_type`, `limit`. |
| `whatsapp_search_contacts` | Search contacts by name or phone (excludes group JIDs). |
| `whatsapp_list_chats` | List chats; optional fuzzy search by chat or participant name. |
| `whatsapp_get_chat` | Get one chat by JID; optional last message. |
| `whatsapp_get_direct_chat_by_contact` | Find direct (1:1) chat for a phone number. |
| `whatsapp_get_contact_chats` | List chats where a contact appears as sender. |
| `whatsapp_list_messages` | Search/filter messages (time, sender, chat, text); BM25 when `query` is set. Default `limit` 200. |
| `whatsapp_get_message_context` | Messages before/after a message ID in the same chat. |
| `whatsapp_get_last_interaction` | Most recent message involving a contact (formatted). |
| `whatsapp_send_message` | Send text via core daemon REST API. |
| `whatsapp_send_file` | Send a local file as media via core daemon. |
| `whatsapp_send_audio_message` | Send audio as a voice message via core daemon. |
| `whatsapp_download_media` | Download media for a message via core daemon. |
| `googledocs_search` | FTS5 search across synced docs; `query`, optional `limit`. |
| `googledocs_get_document` | Full document body by ID. |
| `googledocs_list_recent` | Recently modified docs; optional `limit`. |

**Availability:** `whatsapp_*` tools are registered only when WhatsApp is logged in. `googledocs_*` tools appear when the Google Docs source loads successfully (OAuth credentials baked in at build time). `search` is registered when the search index initializes on MCP startup; if indexing fails, other tools may still be available without `search`.

### Global Search

| Tool | Description |
|------|-------------|
| `search` | Search across all connected data sources by name, participant, or message content. Returns results ranked by hybrid BM25 keyword + semantic vector search with hierarchy weighting (chat names ranked highest, then participants, then message content). Accepts optional `source`, `content_type`, and `limit` parameters. |

Results are returned as JSON with a fixed outer shape and source-specific metadata:

```json
{
  "source": "whatsapp",
  "content_type": "message",
  "title": "Family Chat",
  "content": "Family dinner tonight at 7pm",
  "score": 0.92,
  "metadata": {"message_id": "m4", "chat_jid": "group1@g.us", "sender": "Alice"}
}
```

Metadata shapes per WhatsApp content type:
- **chat_name**: `{"jid", "is_group"}`
- **participant**: `{"jid", "groups"}` — use `jid` with `whatsapp_get_chat` or `whatsapp_list_messages`
- **message**: `{"message_id", "chat_jid", "sender", "timestamp", "is_from_me"}` — use `message_id` with `whatsapp_get_message_context`

### WhatsApp Tools

**Read path**: Queries `messages.db` and `whatsapp.db` directly via SQLite. The core daemon does not need to be running for read-only operations.

**Write path**: Sending messages and downloading media go through the core daemon's REST API at `http://localhost:8080/api`. The core daemon must be running.

#### Contact & Chat Discovery

| Tool | Description |
|------|-------------|
| `whatsapp_search_contacts` | Search contacts by name or phone number. Queries both the local `chats` table and `whatsmeow_contacts` in `whatsapp.db` for broader coverage. Excludes group JIDs. |
| `whatsapp_list_chats` | List chats with optional fuzzy search by chat name or participant name. When a query is provided, uses case-insensitive substring matching plus word-level similarity (threshold 0.6) on chat names, and also searches `whatsmeow_contacts` for matching participant names to find groups where that person is a member. |
| `whatsapp_get_chat` | Get a single chat by JID with optional last message. |
| `whatsapp_get_direct_chat_by_contact` | Find the direct (non-group) chat for a given phone number. |
| `whatsapp_get_contact_chats` | List all chats (including groups) where a contact appears as sender. |

#### Message Reading

| Tool | Description |
|------|-------------|
| `whatsapp_list_messages` | Search and filter messages by time range, sender, chat JID, or text content. When a query is provided, uses BM25 keyword search (FTS5) for relevance-ranked results. Supports pagination and optional surrounding context per message. Default `limit` is 200. |
| `whatsapp_get_message_context` | Get messages before and after a specific message ID within the same chat. |
| `whatsapp_get_last_interaction` | Get the most recent message involving a contact, returned as a formatted string. |

#### Sending

| Tool | Description |
|------|-------------|
| `whatsapp_send_message` | Send a text message to a phone number or group JID. Routes through the core daemon's `/api/send` endpoint. |
| `whatsapp_send_file` | Send a local file (image, video, document) as a media message. The file must be accessible on the machine running the server. |
| `whatsapp_send_audio_message` | Send an audio file as a playable WhatsApp voice message. Non-ogg files require ffmpeg for conversion. |

#### Media

| Tool | Description |
|------|-------------|
| `whatsapp_download_media` | Download media from a received message by `message_id` and `chat_jid`. Routes through the core daemon's `/api/download` endpoint. Returns the local file path. |

### Google Docs Tools

**Read path**: Queries `googledocs.db` directly via SQLite. Documents are synced by the core daemon every 5 minutes. Each sync cycle lists all Google Docs (including shared drives) via the Drive API, fetches content only for new or modified documents, and deletes local rows for documents that have been trashed or deleted remotely. The core daemon does not need to be running for read-only operations.

| Tool | Description |
|------|-------------|
| `googledocs_search` | Full-text search across all Google Docs using FTS5. Returns document snippets with highlighted matches, modification times, and web links. Accepts `query` (required) and `limit` (default 10) parameters. |
| `googledocs_get_document` | Get the full content of a specific Google Doc by ID. Returns title, content, modification time, and web link. |
| `googledocs_list_recent` | List recently modified Google Docs, sorted by modification time descending. Accepts `limit` parameter (default 20). |

### Google Sheets Tools

**Read path**: Queries `googlesheets.db` directly via SQLite. Spreadsheets are synced by the core daemon every 5 minutes. Each sync cycle lists all Google Sheets (including shared drives) via the Drive API, fetches content only for new or modified spreadsheets, and deletes local rows for spreadsheets that have been trashed or deleted remotely. The core daemon does not need to be running for read-only operations. Spreadsheet content is stored as plain text: each sheet is rendered with a `## SheetName` header followed by tab-separated cell values.

| Tool | Description |
|------|-------------|
| `googlesheets_search` | Full-text search across all Google Sheets using FTS5. Returns spreadsheet snippets with highlighted matches, modification times, sheet counts, and web links. Accepts `query` (required) and `limit` (default 10) parameters. |
| `googlesheets_get_spreadsheet` | Get the full content of a specific Google Sheet by ID. Returns title, content, modification time, sheet count, and web link. |
| `googlesheets_list_recent` | List recently modified Google Sheets, sorted by modification time descending. Accepts `limit` parameter (default 20). |

---

## Search

### Global Hybrid Search

The `search` tool combines BM25 keyword search with semantic vector search across a unified search index (`search.db`). On MCP server startup, each `DataSource` provides its indexable content via `SearchEntries()`. Content is normalized into a shared schema:

| Content Type | Source | Indexed From |
|-------------|--------|-------------|
| `chat_name` | WhatsApp | Chat display names |
| `participant` | WhatsApp | Contact names from `whatsmeow_contacts` |
| `message` | WhatsApp | Message content (>20 chars only) |
| `document_title` | Google Docs | Document titles (prefixed with owner names when present) |
| `document_owner` | Google Docs | Document owner names and emails |
| `document_content` | Google Docs | Document text content (prefixed with owner names, chunked at 5000 chars) |
| `spreadsheet_title` | Google Sheets | Spreadsheet titles (prefixed with owner names when present) |
| `spreadsheet_owner` | Google Sheets | Spreadsheet owner names and emails |
| `spreadsheet_content` | Google Sheets | Spreadsheet cell content (prefixed with owner names, chunked at 5000 chars) |

**Search algorithm:**

1. **BM25** — FTS5 full-text search on the `search_fts` virtual table
2. **Vector** (when ONNX installed) — embed the query with BGE-Small-EN-v1.5, compute cosine similarity against stored embeddings
3. **Reciprocal Rank Fusion (RRF)** — combine BM25 and vector ranked lists: `score(d) = sum(1/(k+rank_i))` with k=60
4. **Hierarchy weighting** — multiply fused score by content type: `chat_name` (3x), `participant` (2x), `message` (1x), `document_title` (2x), `document_owner` (2x), `document_content` (1x), `spreadsheet_title` (2x), `spreadsheet_owner` (2x), `spreadsheet_content` (1x)

When ONNX Runtime is not installed, vector search is disabled and the system falls back to BM25-only. This is transparent to the caller.

### BM25 Keyword Search (FTS5)

When `whatsapp_list_messages` is called with a `query` parameter, the server uses SQLite's FTS5 full-text search engine with BM25 scoring. The `messages_fts` virtual table in `messages.db` is maintained via triggers so the index is always in sync. When a `SearchStore` with an embedder is available, the message search combines BM25 with vector results using RRF for improved recall.

Without a `query` parameter, `whatsapp_list_messages` falls back to chronological listing with optional filters.

### Fuzzy Chat & Participant Search

The `whatsapp_list_chats` tool supports fuzzy search across two dimensions when a query is provided:

1. **Chat name matching** — all chat names are compared against the query using case-insensitive substring matching followed by word-level fuzzy matching (LCS-based similarity ratio with a 0.6 threshold). This handles typos like "famly" matching "Family" and partial words like "birth" matching "Birthday Group".

2. **Participant name matching** — the `whatsmeow_contacts` table in `whatsapp.db` is searched for contacts whose `full_name` or `push_name` fuzzy-matches the query. Matching contact JIDs are then looked up in the `group_participants` table to find groups they belong to, plus their direct chat JIDs. Searching for "Kevin" returns Kevin's direct chat and any group where Kevin is a member, even if the group name doesn't contain "Kevin".

For queries shorter than 3 characters, only exact substring matching is used (fuzzy word matching is disabled to avoid false positives). Multi-word queries require each word to fuzzy-match at least one word in the target text.

### Embedding Infrastructure

Semantic vector search uses [fastembed-go](https://github.com/bdombro/fastembed-go) with the BGE-Small-EN-v1.5 ONNX model. The ONNX Runtime shared library is auto-downloaded during `./scripts/install.sh` to `~/.local/share/mcpyeahyouknowme/lib/` (app-local, not exposed to system paths). The embedding model is auto-cached in `~/.local/share/mcpyeahyouknowme/models/` on first use.

Embeddings are pre-computed during MCP server startup for all indexed content and stored in the `search_embeddings` table. Only new/changed entries are embedded on subsequent starts.

---

## Name Resolution

Contact names are resolved through a multi-step lookup, falling through until a non-phone-number name is found:

1. **messages.db `chats` table** — exact JID match
2. **messages.db `chats` table** — LIKE match on the phone number portion
3. **whatsapp.db `whatsmeow_contacts`** — lookup by full JID, `phone@s.whatsapp.net`, or `phone@lid`; returns `full_name` or `push_name`
4. **whatsapp.db LID mapping** — for LID-based senders, maps LID to phone via `whatsmeow_lid_map`, then re-looks up in `whatsmeow_contacts`
5. **Fallback** — raw sender string (phone number or JID)

At each step, results that look like phone numbers (all digits, optional leading `+`) or group placeholder names (`Group 120363...`) are skipped in favour of a more authoritative source. If the stored name is a placeholder and a real name is resolved, the `chats` table is automatically updated. For LID-based senders where no contact name exists, the resolved phone number is returned instead of the opaque LID.

---

## Data Storage

All data is stored in `~/.local/share/mcpyeahyouknowme/`.

| Path | Purpose |
|------|---------|
| `whatsapp.db` | whatsmeow session store (device credentials, contacts, LID mappings) |
| `messages.db` | Application message and chat database (includes FTS5 index) |
| `googledocs.db` | Google Docs documents database (includes FTS5 full-text index) |
| `googledocs_token.json` | OAuth 2.0 token for Google Docs/Drive APIs |
| `googledocs_email.txt` | Cached Google account email (fetched during login via Drive API) |
| `search.db` | Global search index (FTS5 + vector embeddings across all sources) |
| `lib/` | ONNX Runtime shared library (auto-downloaded by `./scripts/install.sh`) |
| `models/` | Cached embedding model (auto-downloaded on first MCP startup) |
| `downloads/` | Downloaded WhatsApp media files |
### messages.db Schema

| Table | Key | Contents |
|-------|-----|----------|
| `chats` | `jid` (primary) | Chat JID, display name, last message timestamp |
| `messages` | `(id, chat_jid)` (composite) | Sender, content, timestamp, `is_from_me`, media metadata (type, filename, URL, encryption keys) |
| `group_participants` | `(group_jid, participant_jid)` (composite) | Maps each group chat to its individual member JIDs, extracted from history sync conversation metadata and WhatsApp's `GetGroupInfo` API |
| `messages_fts` | (FTS5 virtual) | Full-text search index on `messages.content`, maintained via triggers |

Tables are created on startup if they don't exist. The FTS5 index is automatically rebuilt from the messages table on first run. The Go binary must be built with `-tags "sqlite_fts5"` to enable FTS5 support.

### search.db Schema

| Table | Key | Contents |
|-------|-----|----------|
| `search_entries` | `id` (auto), unique(`source`, `source_id`, `content_type`) | Normalized content from all sources: source, ID, type, title, content, JSON metadata, timestamp |
| `search_fts` | (FTS5 virtual) | Full-text search index on `search_entries` title and content, maintained via triggers |
| `search_embeddings` | `entry_id` (foreign key) | Pre-computed vector embeddings (BGE-Small-EN-v1.5, stored as raw float32 bytes) |
| `search_meta` | `source` (primary) | Tracks `last_indexed` timestamp per source for incremental updates |

Populated on MCP server startup from each `DataSource.SearchEntries()`. Incremental: only new/changed entries are added on subsequent starts.

---

## Resilience & Self-Healing

The application must be resilient to transient failures across all connections — database, network, and inter-process. No single failure should crash the daemon or leave the system in a broken state.

### Database Concurrency

Multiple processes access the same SQLite databases concurrently (the `core` daemon writes, `mcp` and CLI commands read). All database connections must follow these rules:

1. **WAL mode** — every database (`messages.db`, `search.db`, `googledocs.db`) must use `PRAGMA journal_mode=WAL` so readers never block writers and vice versa.
2. **Busy timeout** — every connection must set `busy_timeout` to at least **30 seconds** (30000ms). This tells SQLite to retry internally rather than immediately returning `SQLITE_BUSY`. This applies to both connection-string params (`_busy_timeout=30000`) and PRAGMA statements.
3. **Context timeouts** — when a Go `context.WithTimeout` wraps a database call, the context deadline must exceed the busy timeout (e.g. 35s) so SQLite's internal retry has time to succeed before the context cancels.
4. **Read-only where possible** — CLI commands (`info`) and MCP read paths should open databases with `mode=ro` to avoid writer contention entirely.

### Daemon Error Handling

The core daemon runs long-lived services (WhatsApp connection, Google Docs sync). These must not exit on transient errors:

- **WhatsApp message handler** — if `StoreMessage` or `StoreChat` fails (e.g. busy timeout expired), log a warning and continue. The next incoming message will succeed once the lock clears. Never crash the event loop.
- **Google Docs sync** — each cycle does a full Drive metadata listing to detect new, modified, and deleted documents. Content is fetched via the Docs API only for new or modified documents; locally-stored documents absent from the remote listing are deleted. If an individual document fetch or store fails, log a warning and continue to the next document. If the entire sync cycle fails (API error, auth expiry, database lock), log the error and wait for the next ticker interval to retry. Never return a fatal error from `StartCore` for transient issues.
- **WhatsApp reconnection** — whatsmeow handles automatic reconnection on websocket drops. The daemon must not exit on connection errors; it should let whatsmeow's backoff retry logic handle reconnection.

### LaunchAgent & Process Management

The macOS LaunchAgent (`com.mcpyeahyouknowme.core`) is configured with `KeepAlive: true`, meaning launchd restarts the daemon if it exits. This means:

- The daemon must **not** exit on recoverable errors (network, database busy, auth expiry) — otherwise launchd will restart it in a tight loop.
- `scripts/kill.sh` must **unload** the LaunchAgent before killing processes to prevent immediate restart during cleanup. It does not reload the daemon afterward; use `mcpyeahyouknowme start` to bring it back.
- `scripts/install.sh` must **unload** the LaunchAgent before replacing the binary, then reload it after installation.

### Binary Signing (macOS)

macOS Sequoia+ enforces provenance tracking on copied binaries. After `install.sh` copies the built binary to `~/.local/bin/`, it must:

1. Remove the `com.apple.provenance` extended attribute (`xattr -d`)
2. Ad-hoc codesign the binary (`codesign --force --sign -`)

Without this, Gatekeeper blocks execution — the first invocation is killed (SIGKILL), and subsequent invocations hang in uninterruptible kernel wait.

---

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GOOGLE_CLIENT_ID` | Build-time | - | OAuth 2.0 client ID from Google Cloud Console; set in `.env` and baked into the binary via `-ldflags` |
| `GOOGLE_CLIENT_SECRET` | Build-time | - | OAuth 2.0 client secret from Google Cloud Console; set in `.env` and baked into the binary via `-ldflags` |
| `MCP_ENABLE_EMBEDDINGS` | No | `true` | Set to `false` to disable vector search and skip ONNX Runtime |

Google OAuth credentials must be set before building. Copy `.env.example` to `.env` and fill in the values. The build script (`scripts/build.sh`) will abort if they are missing.

### Hardcoded Paths

| Setting | Value |
|---------|-------|
| Data directory | `~/.local/share/mcpyeahyouknowme/` |
| Messages database | `~/.local/share/mcpyeahyouknowme/messages.db` |
| Contacts database | `~/.local/share/mcpyeahyouknowme/whatsapp.db` |
| Core daemon REST API | `http://localhost:8080/api` |

## Dependencies

- [whatsmeow](https://github.com/tulir/whatsmeow) — WhatsApp web multidevice API
- [go-sqlite3](https://github.com/mattn/go-sqlite3) — SQLite driver (requires CGO, built with `sqlite_fts5` tag)
- [mcp-go](https://github.com/mark3labs/mcp-go) — Model Context Protocol server framework
- [qrterminal](https://github.com/mdp/qrterminal) — QR code rendering in terminal
- [fastembed-go](https://github.com/bdombro/fastembed-go) — ONNX-based text embeddings (BGE-Small-EN-v1.5)
- [golang.org/x/oauth2](https://pkg.go.dev/golang.org/x/oauth2) — OAuth 2.0 client library
- [google.golang.org/api](https://pkg.go.dev/google.golang.org/api) — Google APIs (Docs v1, Drive v3)
- **ONNX Runtime** (optional, auto-downloaded) — native shared library for embedding inference, downloaded by `./scripts/install.sh` to `~/.local/share/mcpyeahyouknowme/lib/`
- **ffmpeg** (optional) — required only for automatic audio format conversion in `send_audio_message`
