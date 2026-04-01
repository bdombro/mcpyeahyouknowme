# Gmail Threading Plan

## Goal

Improve Gmail retrieval quality by treating an email thread as the primary MCP read unit while keeping individual messages as the canonical stored records.

This plan does **not** add `gmail_search_threads` yet. The immediate target is:

- keep storing raw Gmail messages
- derive a thread-level representation in SQLite
- reduce duplicated quoted-reply text in indexing
- add a `gmail_get_thread` MCP tool for chat-like thread retrieval

## Current State

Today the Gmail source:

- stores one row per Gmail message in `gmail_messages`
- stores `thread_id` on each message but does not expose a thread-level tool
- stores a single `body_text` per message
- indexes raw `body_text`, which often includes quoted prior replies
- returns only message-level results from `gmail_search`, `gmail_get_message`, and `gmail_list_recent`

This causes search/index duplication and makes the MCP read flow less natural for conversation-style reasoning.

## Design Principles

- Messages remain the source of truth.
- Threads are stored as a derived, rebuildable cache.
- Raw message content is preserved for fidelity and debugging.
- Visible message content should be separated from quoted historical content for indexing.
- Thread retrieval should feel closer to a chat transcript than a mailbox dump.
- Derived data should be recomputable from `gmail_messages` during sync or rebuild.

## Proposed Data Model

### Canonical table

Keep `gmail_messages` as the canonical record for:

- Gmail message ID
- Gmail thread ID
- headers and labels
- timestamps
- attachments metadata
- raw extracted body

Add derived body columns to `gmail_messages`:

- `body_raw` — the currently extracted full body, unchanged except for MIME decoding / HTML stripping
- `body_visible` — best-effort body with quoted prior replies removed

Migration note:

- rename current `body_text` meaning to `body_raw`
- use `body_visible` for indexing and thread reconstruction
- if a true column rename is inconvenient in SQLite migration logic, keep `body_text` temporarily and backfill `body_raw` plus `body_visible`

### Derived thread table

Add a derived `gmail_threads` table keyed by `thread_id`.

Suggested columns:

- `thread_id TEXT PRIMARY KEY`
- `subject TEXT NOT NULL DEFAULT ''`
- `participants TEXT NOT NULL DEFAULT ''`
- `message_count INTEGER NOT NULL DEFAULT 0`
- `first_date TEXT NOT NULL DEFAULT ''`
- `last_date TEXT NOT NULL DEFAULT ''`
- `last_message_id TEXT NOT NULL DEFAULT ''`
- `thread_text_visible TEXT NOT NULL DEFAULT ''`
- `last_synced TEXT NOT NULL`

Purpose of this table:

- fast `gmail_get_thread` lookup
- future thread listing / sorting
- cleaner thread-level global indexing
- rebuildable cache derived entirely from `gmail_messages`

Optional later columns:

- `folders TEXT`
- `labels TEXT`
- `has_attachments INTEGER`
- `participants_json TEXT`

## Derived Thread Construction

For each `thread_id`:

1. Load all messages ordered by parsed date, then by message ID as a stable tiebreaker.
2. Build a normalized participant set from `from_addr`, `to_addrs`, `cc_addrs`, and `bcc_addrs`.
3. Choose a thread subject using the newest non-empty subject, optionally normalized by stripping repeated `Re:` / `Fwd:` prefixes for display.
4. Concatenate `body_visible` values in message order into `thread_text_visible`.
5. Store summary fields such as message count, first date, last date, and last message ID.

The thread table must never be treated as authoritative over the message table. It is a materialized view / cache.

## Quoted Text Handling

### Objective

Avoid indexing the same historical email content repeatedly when each reply includes the full quoted chain.

### Strategy

Preserve both:

- `body_raw` for fidelity
- `body_visible` for thread display and indexing

Treat dedup as **pessimistic, boundary-based stripping**, not aggressive content removal:

- only remove text when it is very likely to be quoted historical material
- otherwise keep the text
- never overwrite or discard `body_raw`
- use `body_visible` only as derived retrieval/index text

### Initial heuristics for `body_visible`

Apply best-effort stripping to `body_raw` using conservative plain-text heuristics:

- stop at lines matching patterns like `On ... wrote:`
- stop at forwarded-message separators
- stop at common header blocks beginning with `From:`, `Sent:`, `To:`, `Subject:`
- drop lines beginning with `>` only when they form a clear quoted block
- trim known mobile signatures only when clearly separated from the authored content

Guidelines:

- prefer false negatives over false positives
- if the stripper is uncertain, keep more text rather than deleting user-authored content
- store the raw text regardless so no information is lost
- avoid fuzzy similarity matching against earlier messages in the first implementation
- avoid stripping content unless a strong reply boundary is present
- avoid removing the entire body unless the message is almost entirely quote material

### Fallback behavior

If stripping produces an empty or suspiciously small result while `body_raw` is large, fall back to:

- `body_visible = body_raw`

This prevents catastrophic loss of useful content from malformed or highly customized email formats.

### Non-goals for v1

The initial implementation should **not** attempt:

- fuzzy dedup across messages in a thread
- longest-common-substring or diff-based overlap removal
- exact parent/child quote attribution between messages
- destructive replacement of canonical stored text

## MCP Tool Changes

### New tool: `gmail_get_thread`

Add a new Gmail MCP tool:

- `gmail_get_thread`

Arguments:

- `thread_id` — required Gmail thread ID
- `include_raw` — optional boolean, default `false`

Response shape:

- `thread_id`
- `subject`
- `participants`
- `message_count`
- `first_date`
- `last_date`
- `messages`

Per-message fields:

- `message_id`
- `from`
- `to`
- `cc`
- `bcc`
- `date`
- `folder`
- `labels`
- `has_attachments`
- `body`
- `body_raw` when `include_raw=true`

Behavior:

- messages returned in ascending chronological order
- default `body` should be `body_visible`
- include enough metadata for follow-up actions like attachment download or message inspection

### Existing tool changes

Update these tools to return `thread_id`:

- `gmail_search`
- `gmail_get_message`
- `gmail_list_recent`

This lets the model pivot from a single relevant message into the whole conversation by calling `gmail_get_thread`.

## Indexing Changes

### Gmail-local FTS

Shift Gmail FTS indexing away from raw duplicated bodies.

Recommended direction:

- keep message-level local storage
- change message-level FTS to index `body_visible` instead of raw full-body text

Possible options:

1. Keep `gmail_messages_fts` on messages, but point it at `body_visible`.
2. Replace or supplement it with `gmail_threads_fts` on `thread_text_visible`.

Recommended first step:

- keep message-level FTS for minimal disruption
- index `body_visible`
- return `thread_id` in results so clients can fetch the thread

### Global search entries

Change Gmail `SearchEntries()` to prefer thread-level content for semantic/global search.

Recommended content types:

- `email_thread_subject`
- `email_thread_participants`
- `email_thread_content`
- optionally keep `email_subject` with lower weight for direct message lookup compatibility

Recommended first step:

- emit thread-level global search entries from `gmail_threads`
- stop chunking raw per-message quoted content into global search entries
- do not globally index message-level Gmail bodies by default once thread-level indexing is in place

This should make cross-source search results feel more like WhatsApp chats and less like repeated mailbox fragments.

### Thread chunking strategy

Indexing should be **thread-scoped** rather than message-scoped.

That means each indexed Gmail content entry should represent:

- a thread-level subject or participant record, or
- a chunk of a reconstructed thread transcript

Chunking rules:

1. Reconstruct the thread in chronological order using `body_visible`.
2. Treat each message as the first natural chunk boundary.
3. Merge adjacent messages into one chunk until a target size is reached.
4. If a single message exceeds the maximum chunk size, split that message internally while preserving `message_id` and part ordering.
5. Optionally start a new chunk when there is a large time gap between adjacent messages in the same thread.

Recommended defaults:

- target chunk size: `3000` characters
- hard max chunk size: `5000` characters
- overlap: none across normal message boundaries
- overlap within a split long message: optional small overlap only if retrieval quality needs it later
- large time gap split threshold: optional, around 7-14 days

Formatting guidance:

- format indexed thread chunks like a lightweight transcript
- include subject and participants at the top when useful
- render each message with timestamp, sender, and `body_visible`

Example shape:

```text
Subject: Project kickoff
Participants: Alice, Bob, Carol

[2026-03-28 09:14] Alice
Can we move the review to Thursday?

[2026-03-28 09:26] Bob
Thursday works for me.
```

Suggested metadata on each `email_thread_content` entry:

- `thread_id`
- `chunk_index`
- `start_message_id`
- `end_message_id`
- `start_date`
- `end_date`
- `subject`

This keeps retrieval conversation-oriented while still letting the model pivot cleanly into `gmail_get_thread`.

## Sync and Rebuild Flow

### Message ingestion

During Gmail sync, for each fetched message:

1. extract `body_raw`
2. derive `body_visible`
3. upsert `gmail_messages`

### Thread materialization

After message sync completes:

1. identify touched `thread_id` values from newly inserted or updated messages
2. rebuild only those derived thread rows
3. remove any `gmail_threads` rows whose underlying messages no longer exist

For simplicity, an initial implementation may rebuild the entire thread table after each sync. If performance is acceptable, that may be good enough to start.

### Reindexing

After thread derivation changes:

- rebuild Gmail FTS
- rebuild or refresh global search entries for Gmail

## Suggested Implementation Phases

### Phase 1: body derivation and MCP pivot

- add `body_raw` and `body_visible` to `gmail_messages`
- backfill existing rows
- update sync to populate both
- add `thread_id` to current Gmail MCP responses
- add `gmail_get_thread` that reconstructs from `gmail_messages`

Outcome:

- immediate better MCP ergonomics
- no derived thread table required yet for correctness

### Phase 2: derived thread table

- add `gmail_threads`
- add rebuild logic from `gmail_messages`
- make `gmail_get_thread` read from `gmail_threads` plus child messages, or reconstruct on-demand as fallback

Outcome:

- cheaper thread retrieval
- foundation for thread-level indexing and listing

### Phase 3: indexing migration

- migrate Gmail-local FTS to `body_visible`
- change global `SearchEntries()` to emit thread-level entries from `gmail_threads`
- add hierarchy weights for thread-level Gmail content types

Outcome:

- much lower quote duplication in retrieval
- more useful search ranking for email conversations

## Testing Plan

Add or update tests for:

- quote stripping heuristics across common plain-text reply formats
- fallback behavior when quote stripping would erase too much
- message sync storing both raw and visible bodies
- thread derivation from multiple messages in chronological order
- `gmail_get_thread` success and not-found behavior
- existing Gmail tools now returning `thread_id`
- global search entries shifting from raw message chunks to thread-level content

Prefer focused fixtures over large real-world message dumps.

## Spec and Docs Updates

When implementation starts, update:

- `docs/spec.md` MCP tools section for `gmail_get_thread`
- `docs/spec.md` database schema for `gmail_messages` and `gmail_threads`
- `docs/spec.md` search behavior for visible-text/thread-level indexing

## Open Questions

- Should `gmail_get_thread` include both normalized display subject and raw subject variants?
- Should thread participants be stored as a comma-separated display field, JSON array, or both?
- Should `gmail_get_thread` expose snippets of stripped quoted sections for debugging?
- Is full-table thread rebuild after each 5-minute sync acceptable, or do we need incremental thread rebuild from the start?
- Should Gmail-local FTS remain message-oriented for exact retrieval even after global search becomes thread-oriented?

## Recommendation

Implement Phase 1 and Phase 2 together if the schema migration cost is modest:

- add `body_raw` / `body_visible`
- add `gmail_threads`
- add `gmail_get_thread`
- return `thread_id` from existing Gmail tools

Then migrate indexing in a follow-up change once the new thread representation proves stable.
