package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Returns an initialized in-memory search store so tests can exercise indexing and querying without touching disk.
func newTestSearchStore(t *testing.T) *SearchStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open test search db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=5000")

	store, err := NewSearchStoreFromDB(db)
	if err != nil {
		t.Fatalf("create search store: %v", err)
	}
	return store
}

// Opens a minimal schema for direct FTS trigger-helper tests without going through the full search-store setup.
func newFTSTriggerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open trigger test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE search_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT,
			source_id TEXT,
			content_type TEXT,
			title TEXT,
			content TEXT,
			metadata TEXT,
			timestamp DATETIME
		);
		CREATE VIRTUAL TABLE search_fts USING fts5(
			title, content,
			content='search_entries',
			content_rowid='id'
		);
	`); err != nil {
		t.Fatalf("create trigger test schema: %v", err)
	}

	return db
}

// Returns representative cross-source search entries so ranking, metadata, and filtering tests share one fixture set.
func seedSearchEntries() []SearchEntry {
	now := time.Now()
	t1 := now.Add(-1 * time.Hour)
	t2 := now.Add(-2 * time.Hour)
	return []SearchEntry{
		{Source: "whatsapp", SourceID: "group1@g.us", ContentType: "chat_name", Title: "Family Chat", Content: "Family Chat", Metadata: json.RawMessage(`{"jid":"group1@g.us","is_group":true}`), Timestamp: &t1},
		{Source: "whatsapp", SourceID: "group2@g.us", ContentType: "chat_name", Title: "Work Team", Content: "Work Team", Metadata: json.RawMessage(`{"jid":"group2@g.us","is_group":true}`), Timestamp: &t2},
		{Source: "whatsapp", SourceID: "11111@s.whatsapp.net", ContentType: "participant", Title: "Alice Smith", Content: "Alice Smith 11111", Metadata: json.RawMessage(`{"jid":"11111@s.whatsapp.net","groups":["group1@g.us"]}`)},
		{Source: "whatsapp", SourceID: "22222@s.whatsapp.net", ContentType: "participant", Title: "Bob Jones", Content: "Bob Jones 22222", Metadata: json.RawMessage(`{"jid":"22222@s.whatsapp.net","groups":["group1@g.us"]}`)},
		{Source: "whatsapp", SourceID: "group1@g.us#chunk:000", ContentType: "chat_content", Title: "Family Chat", Content: "Chat: Family Chat\n\n[2025-01-01T10:00:00Z] Alice Smith\nFamily dinner tonight at seven", Metadata: json.RawMessage(`{"chat_jid":"group1@g.us","chunk_index":0,"start_message_id":"m4","end_message_id":"m4"}`)},
		{Source: "whatsapp", SourceID: "group2@g.us#chunk:000", ContentType: "chat_content", Title: "Work Team", Content: "Chat: Work Team\n\n[2025-01-01T11:00:00Z] Charlie Brown\nMeeting at three pm tomorrow", Metadata: json.RawMessage(`{"chat_jid":"group2@g.us","chunk_index":0,"start_message_id":"m7","end_message_id":"m7"}`)},
	}
}

// ---------- Schema & Indexing ----------

// Verifies indexing inserts all entries into the shared search table.
func TestSearchStore_IndexEntries(t *testing.T) {
	store := newTestSearchStore(t)
	entries := seedSearchEntries()
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM search_entries").Scan(&count)
	if count != len(entries) {
		t.Errorf("expected %d entries, got %d", len(entries), count)
	}
}

// Verifies upserts replace existing content without duplicating rows.
func TestSearchStore_IndexEntries_upsert(t *testing.T) {
	store := newTestSearchStore(t)
	entries := seedSearchEntries()
	store.IndexEntries(entries)

	entries[0].Content = "Updated Family Chat"
	store.IndexEntries(entries[:1])

	var content string
	store.db.QueryRow("SELECT content FROM search_entries WHERE source_id = ?", "group1@g.us").Scan(&content)
	if content != "Updated Family Chat" {
		t.Errorf("expected updated content, got %q", content)
	}

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM search_entries").Scan(&count)
	if count != len(entries) {
		t.Errorf("expected %d entries after upsert, got %d", len(entries), count)
	}
}

// Verifies DeleteBySource removes only one source's rows and leaves the rest searchable through FTS.
func TestSearchStore_DeleteBySource(t *testing.T) {
	store := newTestSearchStore(t)
	entries := []SearchEntry{
		{Source: "gsuite", SourceID: "thread-1", ContentType: "email_thread_subject", Title: "John Thomas", Content: "John Thomas has 3 kids"},
		{Source: "notebook", SourceID: "note-1", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"},
		{Source: "notebook", SourceID: "note-1#chunk0", ContentType: "note_content", Title: "John Thomas", Content: "John Thomas has 2 kids"},
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	if err := store.DeleteBySource("gsuite"); err != nil {
		t.Fatalf("DeleteBySource: %v", err)
	}

	var gsuiteCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'gsuite'`).Scan(&gsuiteCount); err != nil {
		t.Fatalf("count gsuite rows: %v", err)
	}
	if gsuiteCount != 0 {
		t.Fatalf("expected gsuite rows to be deleted, got %d", gsuiteCount)
	}
	var notebookCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'notebook'`).Scan(&notebookCount); err != nil {
		t.Fatalf("count notebook rows: %v", err)
	}
	if notebookCount != 2 {
		t.Fatalf("expected notebook rows to remain, got %d", notebookCount)
	}

	results, err := store.Search("John Thomas", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected notebook results to remain searchable")
	}
	for _, result := range results {
		if result.Source == "gsuite" {
			t.Fatalf("expected gsuite results to be deleted, got %#v", results)
		}
	}
}

// Verifies DeleteBySource returns an error when the underlying DB handle is already closed.
func TestSearchStore_DeleteBySource_closedDB(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	store, err := NewSearchStoreFromDB(db)
	if err != nil {
		t.Fatalf("NewSearchStoreFromDB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := store.DeleteBySource("gsuite"); err == nil {
		t.Fatal("expected closed DB error")
	}
}

// Verifies PruneSourceKeys removes stale rows that no longer appear in a source's latest SearchEntries output.
func TestSearchStore_PruneSourceKeys_subset(t *testing.T) {
	store := newTestSearchStore(t)
	entries := []SearchEntry{
		{Source: "notebook", SourceID: "note-1", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"},
		{Source: "notebook", SourceID: "note-1#chunk0", ContentType: "note_content", Title: "John Thomas", Content: "John Thomas has 2 kids"},
		{Source: "notebook", SourceID: "note-2", ContentType: "note_title", Title: "Old note", Content: "Old note"},
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	current := []indexKey{
		{SourceID: "note-1", ContentType: "note_title"},
		{SourceID: "note-1#chunk0", ContentType: "note_content"},
	}
	if err := store.PruneSourceKeys("notebook", current); err != nil {
		t.Fatalf("PruneSourceKeys: %v", err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'notebook'`).Scan(&count); err != nil {
		t.Fatalf("count notebook rows: %v", err)
	}
	if count != len(current) {
		t.Fatalf("expected %d notebook rows after prune, got %d", len(current), count)
	}
	results, err := store.Search("Old note", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected stale note to be pruned, got %#v", results)
	}
}

// Verifies PruneSourceKeys deletes every row for a source when that source returns no current SearchEntries.
func TestSearchStore_PruneSourceKeys_empty(t *testing.T) {
	store := newTestSearchStore(t)
	entries := []SearchEntry{
		{Source: "notebook", SourceID: "note-1", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"},
		{Source: "notebook", SourceID: "note-1#chunk0", ContentType: "note_content", Title: "John Thomas", Content: "John Thomas has 2 kids"},
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	if err := store.PruneSourceKeys("notebook", nil); err != nil {
		t.Fatalf("PruneSourceKeys: %v", err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'notebook'`).Scan(&count); err != nil {
		t.Fatalf("count notebook rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected notebook rows to be deleted, got %d", count)
	}
}

// Verifies PruneSourceKeys returns an error when the DB handle is closed before prune bookkeeping can begin.
func TestSearchStore_PruneSourceKeys_closedDB(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	store, err := NewSearchStoreFromDB(db)
	if err != nil {
		t.Fatalf("NewSearchStoreFromDB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := store.PruneSourceKeys("notebook", []indexKey{{SourceID: "note-1", ContentType: "note_title"}}); err == nil {
		t.Fatal("expected closed DB error")
	}
}

// ---------- BM25 Search ----------

// Verifies BM25 search returns expected matches and content types for a simple keyword query.
func TestSearchStore_BM25Search(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Family", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 'Family'")
	}
	found := false
	for _, r := range results {
		if r.Title == "Family Chat" {
			found = true
			if r.ContentType != "chat_name" && r.ContentType != "chat_content" {
				t.Errorf("unexpected content_type: %s", r.ContentType)
			}
		}
	}
	if !found {
		t.Error("expected to find 'Family Chat' in results")
	}
}

// Verifies tokenized BM25 search matches multi-word queries spanning title and content terms.
func TestSearchStore_BM25Search_multiWord(t *testing.T) {
	store := newTestSearchStore(t)
	if err := store.IndexEntries([]SearchEntry{
		{Source: "notebook", SourceID: "john-thomas", ContentType: "note_content", Title: "John Thomas", Content: "Moved in 2022 for sake of 2 daughters"},
	}); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	results, err := store.Search("John Thomas daughters", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected result for tokenized multi-word query")
	}
	if results[0].Title != "John Thomas" {
		t.Fatalf("expected John Thomas result first, got %q", results[0].Title)
	}
}

// Verifies FTS sanitization quotes per-word tokens and drops punctuation that would force exact phrases.
func TestSanitizeFTSQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "multi word", query: "John Thomas", want: `"John"* OR "Thomas"*`},
		{name: "special chars", query: "john@example.com", want: `"john"* OR "example"* OR "com"*`},
		{name: "quotes and punctuation", query: `"John", Thomas!?`, want: `"John"* OR "Thomas"*`},
		{name: "empty", query: "", want: `""`},
		{name: "single word", query: "Family", want: `"Family"*`},
		{name: "short tokens filtered", query: "birthday dinner at me", want: `"birthday"* OR "dinner"* OR "at"* OR "me"*`},
		{name: "two char tokens kept", query: "AI Go JS", want: `"AI"* OR "Go"* OR "JS"*`},
		{name: "all short tokens fall back", query: "a b", want: `"a"* OR "b"*`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeFTSQuery(tt.query); got != tt.want {
				t.Fatalf("sanitizeFTSQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

// Verifies BM25 search returns no rows for a keyword absent from the index.
func TestSearchStore_BM25Search_noResults(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("zzzznonexistent", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// Verifies source filtering constrains BM25 results to the requested source.
func TestSearchStore_BM25Search_sourceFilter(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("dinner", 10, "whatsapp", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Source != "whatsapp" {
			t.Errorf("expected source=whatsapp, got %s", r.Source)
		}
	}
}

// Verifies content-type filtering constrains BM25 results to the requested type.
func TestSearchStore_BM25Search_typeFilter(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Family", 10, "", "chat_name", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.ContentType != "chat_name" {
			t.Errorf("expected content_type=chat_name, got %s", r.ContentType)
		}
	}
}

// ---------- Hierarchy Weighting ----------

// Verifies hierarchy weights lift chat names above weaker participant and chat-content matches.
func TestSearchStore_HierarchyWeighting(t *testing.T) {
	store := newTestSearchStore(t)

	now := time.Now()
	entries := []SearchEntry{
		{Source: "whatsapp", SourceID: "family-chat", ContentType: "chat_name", Title: "Family", Content: "Family", Timestamp: &now},
		{Source: "whatsapp", SourceID: "alice-family", ContentType: "participant", Title: "Family Alice", Content: "Family Alice", Timestamp: &now},
		{Source: "whatsapp", SourceID: "family-chat#chunk:000", ContentType: "chat_content", Title: "Chat", Content: "Family dinner tonight", Timestamp: &now},
	}
	store.IndexEntries(entries)

	results, err := store.Search("Family", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// chat_name should rank highest due to 3x weight
	if results[0].ContentType != "chat_name" {
		t.Errorf("expected chat_name to rank first, got %s (score=%.4f)", results[0].ContentType, results[0].Score)
	}
}

// ---------- Metadata hint ----------

// Verifies metadata-hint lookup returns guidance for known pairs and stays empty for unknown content.
func TestSearchMetadataHint_knownAndUnknown(t *testing.T) {
	if got := searchMetadataHint("whatsapp", "chat_content"); !strings.Contains(got, "start_message_id") {
		t.Fatalf("expected whatsapp chat-content hint, got %q", got)
	}
	if got := searchMetadataHint("unknown", "type"); got != "" {
		t.Fatalf("expected empty hint for unknown content, got %q", got)
	}
}

// ---------- Source Timestamp Tracking ----------

// Verifies per-source last-indexed timestamps can be written and read back accurately.
func TestSearchStore_SourceTimestamp(t *testing.T) {
	store := newTestSearchStore(t)
	now := time.Now().Truncate(time.Second)

	store.UpdateSourceTimestamp("whatsapp", now)
	got := store.LastIndexed("whatsapp")
	if got.Unix() != now.Unix() {
		t.Errorf("expected %v, got %v", now, got)
	}

	missing := store.LastIndexed("gmail")
	if !missing.IsZero() {
		t.Errorf("expected zero time for missing source, got %v", missing)
	}
}

// ---------- Search with default limit ----------

// Verifies non-positive limits fall back to the default result cap.
func TestSearchStore_DefaultLimit(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Family", 0, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// limit=0 should default to 20, which is more than our entries
	if len(results) == 0 {
		t.Error("expected results with default limit")
	}
}

// ---------- Search result limit ----------

// Verifies Search truncates ranked results to the requested limit.
func TestSearchStore_Search_limitTruncates(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Family Chat", 1, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) > 1 {
		t.Errorf("expected at most 1 result, got %d", len(results))
	}
}

// ---------- BM25 search ----------

// Verifies BM25 search returns ranked results.
func TestSearchStore_BM25Only(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("dinner", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected BM25 results")
	}
}

// ---------- Metadata in results ----------

// Verifies search results expose parsed metadata needed for follow-up tool calls.
func TestSearchStore_ResultMetadata(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Alice", 10, "", "participant", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 'Alice'")
	}
	for _, r := range results {
		if r.Metadata == nil {
			t.Error("expected non-nil metadata")
			continue
		}
		var meta map[string]interface{}
		if err := json.Unmarshal(r.Metadata, &meta); err != nil {
			t.Errorf("invalid metadata JSON: %v", err)
		}
		if _, ok := meta["jid"]; !ok {
			t.Error("expected 'jid' in participant metadata")
		}
	}
}

// ---------- NewSearchStore (file-backed) ----------

// Verifies the file-backed constructor creates a usable on-disk search database.
func TestNewSearchStore(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewSearchStore(tmpDir)
	if err != nil {
		t.Fatalf("NewSearchStore: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	dbPath := filepath.Join(tmpDir, "search.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("database file not created at %s", dbPath)
	}

	entries := seedSearchEntries()
	if err := store.IndexEntries(entries[:1]); err != nil {
		t.Errorf("IndexEntries: %v", err)
	}

	store.Close()
}

// ---------- Close ----------

// Verifies Close shuts down the underlying database connection and future queries fail.
func TestSearchStore_Close(t *testing.T) {
	store := newTestSearchStore(t)

	err := store.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	_, err = store.db.Query("SELECT 1")
	if err == nil {
		t.Error("expected error querying closed db")
	}
}

// ---------- Edge cases ----------

// Verifies entries with empty title and content still persist without crashing indexing.
func TestSearchStore_IndexEntries_emptyContent(t *testing.T) {
	store := newTestSearchStore(t)

	entries := []SearchEntry{
		{Source: "test", SourceID: "empty", ContentType: "message", Title: "", Content: ""},
	}

	err := store.IndexEntries(entries)
	if err != nil {
		t.Fatalf("IndexEntries with empty content: %v", err)
	}

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM search_entries WHERE source_id = 'empty'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 entry, got %d", count)
	}
}

// Verifies indexing an empty slice is a no-op rather than an error.
func TestSearchStore_IndexEntries_emptySlice(t *testing.T) {
	store := newTestSearchStore(t)
	if err := store.IndexEntries([]SearchEntry{}); err != nil {
		t.Fatalf("IndexEntries empty slice: %v", err)
	}
}

// Verifies bulk indexing defers FTS row maintenance until one final rebuild, then restores live trigger updates.
func TestSearchStore_BulkIndex_rebuildsFTSAtEnd(t *testing.T) {
	store := newTestSearchStore(t)
	entries := seedSearchEntries()
	if err := store.BeginBulkIndex(); err != nil {
		t.Fatalf("BeginBulkIndex: %v", err)
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	beforeResults, err := store.Search("Family", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search before EndBulkIndex: %v", err)
	}
	if len(beforeResults) != 0 {
		t.Fatalf("expected deferred FTS writes during bulk load, got %d search hits", len(beforeResults))
	}

	if err := store.EndBulkIndex(); err != nil {
		t.Fatalf("EndBulkIndex: %v", err)
	}

	afterResults, err := store.Search("Family", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search after EndBulkIndex: %v", err)
	}
	if len(afterResults) == 0 {
		t.Fatal("expected rebuilt FTS index to return Family results")
	}

	extra := SearchEntry{Source: "test", SourceID: "after-bulk", ContentType: "message", Title: "after", Content: "bulk"}
	if err := store.IndexEntries([]SearchEntry{extra}); err != nil {
		t.Fatalf("IndexEntries after bulk mode: %v", err)
	}

	finalResults, err := store.Search("after", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search after restored triggers: %v", err)
	}
	if len(finalResults) != 1 || finalResults[0].Title != "after" {
		t.Fatalf("expected restored triggers to index post-bulk writes, got %+v", finalResults)
	}
}

// Verifies repeated BeginBulkIndex calls are harmless once FTS maintenance is already suspended.
func TestSearchStore_BeginBulkIndex_noopWhenAlreadyActive(t *testing.T) {
	store := newTestSearchStore(t)
	if err := store.BeginBulkIndex(); err != nil {
		t.Fatalf("first BeginBulkIndex: %v", err)
	}
	if err := store.BeginBulkIndex(); err != nil {
		t.Fatalf("second BeginBulkIndex: %v", err)
	}
}

// Verifies EndBulkIndex is a no-op when bulk FTS mode was never enabled.
func TestSearchStore_EndBulkIndex_noopWhenInactive(t *testing.T) {
	store := newTestSearchStore(t)
	if err := store.EndBulkIndex(); err != nil {
		t.Fatalf("EndBulkIndex inactive: %v", err)
	}
}

// Verifies bulk-index trigger helpers report actionable errors when the search DB handle is unavailable.
func TestSearchStore_BulkIndex_closedDBErrors(t *testing.T) {
	store := newTestSearchStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := store.BeginBulkIndex(); err == nil {
		t.Fatal("expected BeginBulkIndex to fail on closed DB")
	}

	store = newTestSearchStore(t)
	store.bulkFTS = true
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := store.EndBulkIndex(); err == nil {
		t.Fatal("expected EndBulkIndex to fail on closed DB")
	}
}

// Verifies EndBulkIndex surfaces rebuild failures after trigger restoration so callers can stop on a broken FTS pass.
func TestSearchStore_EndBulkIndex_rebuildError(t *testing.T) {
	store := newTestSearchStore(t)
	store.bulkFTS = true

	originalRebuild := searchStoreRebuildFTS
	defer func() {
		searchStoreRebuildFTS = originalRebuild
	}()
	searchStoreRebuildFTS = func(_ *sql.DB) error {
		return errors.New("boom")
	}

	if err := store.EndBulkIndex(); err == nil {
		t.Fatal("expected EndBulkIndex to surface rebuild error")
	}
}

// Verifies createSearchFTSTriggers reports the delete-trigger failure path when the second exec fails.
func TestCreateSearchFTSTriggers_deleteError(t *testing.T) {
	db := newFTSTriggerTestDB(t)
	originalExec := searchStoreExecSQL
	defer func() {
		searchStoreExecSQL = originalExec
	}()

	calls := 0
	searchStoreExecSQL = func(db *sql.DB, statement string) error {
		calls++
		if calls == 2 {
			return errors.New("boom")
		}
		return originalExec(db, statement)
	}

	err := createSearchFTSTriggers(db)
	if err == nil || !strings.Contains(err.Error(), "search_fts_delete") {
		t.Fatalf("expected search_fts_delete failure, got %v", err)
	}
}

// Verifies createSearchFTSTriggers reports the update-trigger failure path when the third exec fails.
func TestCreateSearchFTSTriggers_updateError(t *testing.T) {
	db := newFTSTriggerTestDB(t)
	originalExec := searchStoreExecSQL
	defer func() {
		searchStoreExecSQL = originalExec
	}()

	calls := 0
	searchStoreExecSQL = func(db *sql.DB, statement string) error {
		calls++
		if calls == 3 {
			return errors.New("boom")
		}
		return originalExec(db, statement)
	}

	err := createSearchFTSTriggers(db)
	if err == nil || !strings.Contains(err.Error(), "search_fts_update") {
		t.Fatalf("expected search_fts_update failure, got %v", err)
	}
}

// ---------- Recency boost ----------

// Verifies recencyMultiplier returns 1.0 for nil, ~6x for very recent, ~3.1x for one week ago,
// ~1.9x for one year ago (substantially above ~1x for ten years ago), and is clamped so future
// timestamps behave like "now".
func TestRecencyMultiplier(t *testing.T) {
	now := time.Now()

	if got := recencyMultiplier(nil); got != 1.0 {
		t.Errorf("nil ts: got %.4f, want 1.0", got)
	}

	recent := now.Add(-time.Minute)
	if got := recencyMultiplier(&recent); got <= 5.9 || got > 6.1 {
		t.Errorf("1 minute ago: got %.4f, want ~6x (5.9–6.1)", got)
	}

	oneWeekAgo := now.Add(-7 * 24 * time.Hour)
	if got := recencyMultiplier(&oneWeekAgo); got < 2.8 || got > 3.4 {
		t.Errorf("1 week ago: got %.4f, want ~3.1x (2.8–3.4)", got)
	}

	oneYearAgo := now.Add(-365 * 24 * time.Hour)
	if got := recencyMultiplier(&oneYearAgo); got < 1.6 || got > 2.2 {
		t.Errorf("1 year ago: got %.4f, want ~1.9x (1.6–2.2)", got)
	}

	tenYearsAgo := now.Add(-10 * 365 * 24 * time.Hour)
	if got := recencyMultiplier(&tenYearsAgo); got >= 1.1 {
		t.Errorf("10 years ago: got %.4f, want < 1.1 (~1x)", got)
	}

	// One year must rank substantially above ten years.
	yr := recencyMultiplier(&oneYearAgo)
	tyr := recencyMultiplier(&tenYearsAgo)
	if yr < tyr*1.5 {
		t.Errorf("1 year (%.4f) should be at least 1.5x higher than 10 years (%.4f)", yr, tyr)
	}

	future := now.Add(24 * time.Hour)
	if got := recencyMultiplier(&future); got < 5.9 {
		t.Errorf("future ts (clamped to 0 days): got %.4f, want >= 5.9 (near 6x)", got)
	}
}

// Verifies a recently-timestamped entry outranks an older entry with identical BM25 relevance.
func TestSearchStore_RecencyBoost(t *testing.T) {
	store := newTestSearchStore(t)

	now := time.Now()
	old := now.Add(-365 * 24 * time.Hour)

	entries := []SearchEntry{
		{Source: "test", SourceID: "old-entry", ContentType: "note_content", Title: "Old Message", Content: "mac and cheese", Timestamp: &old},
		{Source: "test", SourceID: "new-entry", ContentType: "note_content", Title: "New Message", Content: "mac and cheese", Timestamp: &now},
	}
	store.IndexEntries(entries)

	results, err := store.Search("mac cheese", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "New Message" {
		t.Errorf("expected recent entry to rank first, got %q (score=%.4f vs %.4f)", results[0].Title, results[0].Score, results[1].Score)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("expected recent entry score %.4f > old entry score %.4f", results[0].Score, results[1].Score)
	}
}

// Verifies a very recent entry with weaker BM25 relevance still outranks stale
// entries that would otherwise displace it before the recency boost is applied.
func TestSearchStore_RecencyBoost_BeatsBM25Cutoff(t *testing.T) {
	store := newTestSearchStore(t)

	now := time.Now()
	old := now.Add(-5 * 365 * 24 * time.Hour)

	// Fill limit=2 slots with old, high-BM25 entries (repeat keyword many times).
	entries := []SearchEntry{
		{Source: "test", SourceID: "old-1", ContentType: "note_content", Title: "Old High BM25 1",
			Content: "mac cheese mac cheese mac cheese mac cheese mac cheese", Timestamp: &old},
		{Source: "test", SourceID: "old-2", ContentType: "note_content", Title: "Old High BM25 2",
			Content: "mac cheese mac cheese mac cheese mac cheese mac cheese", Timestamp: &old},
		// Recent entry with weaker keyword match — should beat old entries via recency.
		{Source: "test", SourceID: "new-weak", ContentType: "note_content", Title: "Recent Weak BM25",
			Content: "mac cheese", Timestamp: &now},
	}
	store.IndexEntries(entries)

	results, err := store.Search("mac cheese", 2, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	found := false
	for _, r := range results {
		if r.Title == "Recent Weak BM25" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected recent entry to appear in top-2 after recency boost; got %q and %q",
			results[0].Title, results[1].Title)
	}
}

// ---------- loadResults unknown content type ----------

// Verifies unknown content types fall back to neutral weighting instead of zeroing scores.
func TestSearchStore_loadResults_unknownContentType(t *testing.T) {
	store := newTestSearchStore(t)
	entries := []SearchEntry{
		{Source: "test", SourceID: "u1", ContentType: "unknown_type", Title: "Unique Test", Content: "unique test content"},
	}
	store.IndexEntries(entries)

	results, err := store.Search("unique", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for unknown content type")
	}
	if results[0].Score == 0 {
		t.Error("expected non-zero score with fallback weight 1.0")
	}
}

// ---------- IndexStats ----------

// Verifies index stats report the entry count and FTS health from the live store.
func TestSearchStore_IndexStats(t *testing.T) {
	store := newTestSearchStore(t)
	entries := seedSearchEntries()
	store.IndexEntries(entries)

	stats := store.IndexStats()
	if stats.Entries != len(entries) {
		t.Errorf("Entries = %d, want %d", stats.Entries, len(entries))
	}
	if !stats.FTSHealthy {
		t.Errorf("FTSHealthy = false after indexing, want true")
	}
}

// Verifies FTSHealthy is true when both counts are zero (empty store is healthy).
func TestSearchStore_IndexStats_emptyIsHealthy(t *testing.T) {
	store := newTestSearchStore(t)
	stats := store.IndexStats()
	if stats.Entries != 0 {
		t.Errorf("Entries = %d, want 0", stats.Entries)
	}
	if !stats.FTSHealthy {
		t.Error("FTSHealthy = false for empty store, want true")
	}
}

// Verifies Clear removes indexed rows and source timestamps so full rebuilds restart from zero.
func TestSearchStore_Clear(t *testing.T) {
	store := newTestSearchStore(t)
	entries := seedSearchEntries()
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}
	store.UpdateSourceTimestamp("test", time.Now())

	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	stats := store.IndexStats()
	if stats.Entries != 0 {
		t.Fatalf("expected cleared stats, got %+v", stats)
	}

	var metaCount int
	store.db.QueryRow("SELECT COUNT(*) FROM search_meta").Scan(&metaCount)
	if metaCount != 0 {
		t.Fatalf("expected cleared search_meta, got %d rows", metaCount)
	}
}

// Verifies Clear reports a useful error when the underlying DB handle is unavailable.
func TestSearchStore_Clear_closedDB(t *testing.T) {
	store := newTestSearchStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := store.Clear()
	if err == nil || !strings.Contains(err.Error(), "clear search index") {
		t.Fatalf("Clear error = %v", err)
	}
}

// Verifies Search returns an error (not empty results) when the underlying DB is closed.
func TestSearchStore_Search_closedDBReturnsError(t *testing.T) {
	store := newTestSearchStore(t)
	store.IndexEntries(seedSearchEntries())
	store.Close()

	_, err := store.Search("Family", 10, "", "", "", "")
	if err == nil {
		t.Fatal("expected error from Search on closed DB, got nil")
	}
}

// Verifies UpdateSourceTimestamp does not panic when the DB is closed (it logs and continues).
func TestSearchStore_UpdateSourceTimestamp_closedDB(t *testing.T) {
	store := newTestSearchStore(t)
	store.Close()
	// Should not panic; logs the error and returns.
	store.UpdateSourceTimestamp("test", time.Now())
}

// Verifies LastIndexed returns zero time (not a panic) when the DB is closed.
func TestSearchStore_LastIndexed_closedDB(t *testing.T) {
	store := newTestSearchStore(t)
	store.Close()
	got := store.LastIndexed("test")
	if !got.IsZero() {
		t.Errorf("expected zero time from LastIndexed on closed DB, got %v", got)
	}
}

// Verifies read-only stats return zeros when no search database exists yet.
func TestReadOnlySearchIndexStats_noDB(t *testing.T) {
	stats := ReadOnlySearchIndexStats(t.TempDir())
	if stats.Entries != 0 {
		t.Errorf("expected 0 entries for non-existent DB, got %d", stats.Entries)
	}
}

// ---------- rebuildFTSIfNeeded ----------

// Verifies rebuildFTSIfNeeded returns an error when the DB is closed before counting entries.
func TestSearchStore_rebuildFTSIfNeeded_closedDB(t *testing.T) {
	store := newTestSearchStore(t)
	// Insert an entry directly, bypassing FTS triggers so search_entries has rows but search_fts does not.
	store.db.Exec("INSERT INTO search_entries (source, source_id, content_type, title, content) VALUES ('test', 'x1', 'note_content', 'T', 'T')")
	store.db.Close()

	err := store.rebuildFTSIfNeeded()
	if err == nil || !strings.Contains(err.Error(), "count search entries") {
		t.Fatalf("expected 'count search entries' error, got %v", err)
	}
}

// Verifies rebuildFTSIfNeeded returns an error when the FTS table is missing.
func TestSearchStore_rebuildFTSIfNeeded_droppedFTSTable(t *testing.T) {
	store := newTestSearchStore(t)
	store.db.Exec("INSERT INTO search_entries (source, source_id, content_type, title, content) VALUES ('test', 'x1', 'note_content', 'T', 'T')")
	store.db.Exec("DROP TABLE IF EXISTS search_fts")

	err := store.rebuildFTSIfNeeded()
	if err == nil || !strings.Contains(err.Error(), "count fts rows") {
		t.Fatalf("expected 'count fts rows' error, got %v", err)
	}
}

// Verifies IndexEntries logs a warning (but does not fail) when rebuildFTSIfNeeded returns an error.
// Drops FTS triggers and the FTS table so rebuildFTSIfNeeded hits the "count fts rows" error path,
// which IndexEntries surfaces as a warning without propagating to callers.
func TestSearchStore_IndexEntries_rebuildFTSWarning(t *testing.T) {
	store := newTestSearchStore(t)
	// Drop FTS triggers first so IndexEntries upserts succeed without the FTS table.
	if err := store.BeginBulkIndex(); err != nil {
		t.Fatalf("BeginBulkIndex: %v", err)
	}
	store.db.Exec("DROP TABLE IF EXISTS search_fts")
	store.bulkFTS = false

	// IndexEntries should succeed; rebuildFTSIfNeeded failure is only a warning.
	if err := store.IndexEntries([]SearchEntry{{Source: "test", SourceID: "x1", ContentType: "note_content", Title: "U", Content: "U"}}); err != nil {
		t.Fatalf("IndexEntries should not propagate rebuild warning, got: %v", err)
	}
}

// Verifies LastIndexed returns zero time and logs when a stored timestamp cannot be parsed.
func TestSearchStore_LastIndexed_invalidTimestamp(t *testing.T) {
	store := newTestSearchStore(t)
	store.db.Exec("INSERT INTO search_meta (source, last_indexed) VALUES ('test', 'not-a-date')")

	got := store.LastIndexed("test")
	if !got.IsZero() {
		t.Errorf("expected zero time for invalid timestamp, got %v", got)
	}
}

// Verifies read-only stats can inspect a populated on-disk search database.
func TestReadOnlySearchIndexStats_withDB(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSearchStore(dir)
	if err != nil {
		t.Fatalf("NewSearchStore: %v", err)
	}
	store.IndexEntries(seedSearchEntries())
	store.Close()

	stats := ReadOnlySearchIndexStats(dir)
	if stats.Entries == 0 {
		t.Error("expected non-zero entries")
	}
}

// ---------- Future Proximity Multiplier ----------

// Verifies futureProximityMultiplier returns 1.0 for nil, gives future dates a higher boost
// than equally-distant past dates, and decays with distance from now.
func TestFutureProximityMultiplier(t *testing.T) {
	now := time.Now()

	if got := futureProximityMultiplier(nil); got != 1.0 {
		t.Errorf("nil ts: got %.4f, want 1.0", got)
	}

	tomorrow := now.Add(24 * time.Hour)
	yesterday := now.Add(-24 * time.Hour)
	tomorrowVal := futureProximityMultiplier(&tomorrow)
	yesterdayVal := futureProximityMultiplier(&yesterday)
	if tomorrowVal <= yesterdayVal {
		t.Errorf("future (%.4f) should be higher than equally-distant past (%.4f)", tomorrowVal, yesterdayVal)
	}
	if tomorrowVal < 6.0 {
		t.Errorf("tomorrow: got %.4f, want >= 6x", tomorrowVal)
	}
	if yesterdayVal < 4.0 {
		t.Errorf("yesterday: got %.4f, want >= 4x", yesterdayVal)
	}

	nextWeek := now.Add(7 * 24 * time.Hour)
	lastWeek := now.Add(-7 * 24 * time.Hour)
	if futureProximityMultiplier(&nextWeek) <= futureProximityMultiplier(&lastWeek) {
		t.Error("next week should outrank last week")
	}

	nextMonth := now.Add(30 * 24 * time.Hour)
	lastMonth := now.Add(-30 * 24 * time.Hour)
	if futureProximityMultiplier(&nextMonth) <= futureProximityMultiplier(&lastMonth) {
		t.Error("next month should outrank last month")
	}

	// Events far in the future still rank above distant past.
	distantFuture := now.Add(365 * 24 * time.Hour)
	distantPast := now.Add(-365 * 24 * time.Hour)
	if futureProximityMultiplier(&distantFuture) <= futureProximityMultiplier(&distantPast) {
		t.Error("distant future should outrank distant past")
	}
}

// Verifies calendar entries use the future-biased proximity multiplier so upcoming events
// outrank past events with identical BM25 relevance.
func TestSearchStore_CalendarFutureBoost(t *testing.T) {
	store := newTestSearchStore(t)
	now := time.Now()
	future := now.Add(48 * time.Hour)
	past := now.Add(-30 * 24 * time.Hour)

	entries := []SearchEntry{
		{Source: "gsuite", SourceID: "past-event", ContentType: "calendar_event",
			Title: "Past standup", Content: "Past standup daily meeting", Timestamp: &past},
		{Source: "gsuite", SourceID: "future-event", ContentType: "calendar_event",
			Title: "Future standup", Content: "Future standup daily meeting", Timestamp: &future},
	}
	store.IndexEntries(entries)

	results, err := store.Search("standup", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Future standup" {
		t.Errorf("expected future event to rank first, got %q (scores: %.4f vs %.4f)",
			results[0].Title, results[0].Score, results[1].Score)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("future event score %.4f should exceed past event score %.4f",
			results[0].Score, results[1].Score)
	}
}

// ---------- Task Status Multiplier ----------

// Verifies taskStatusMultiplier returns 1.5x for needsAction, 0.6x for completed, and 1.0 for others.
func TestTaskStatusMultiplier(t *testing.T) {
	cases := []struct {
		name string
		meta json.RawMessage
		want float64
	}{
		{"needsAction", json.RawMessage(`{"status":"needsAction"}`), 1.5},
		{"completed", json.RawMessage(`{"status":"completed"}`), 0.6},
		{"other", json.RawMessage(`{"status":"someOtherStatus"}`), 1.0},
		{"missing field", json.RawMessage(`{}`), 1.0},
		{"nil", nil, 1.0},
		{"invalid json", json.RawMessage(`{not json}`), 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := taskStatusMultiplier(tc.meta)
			if got != tc.want {
				t.Errorf("taskStatusMultiplier(%s): got %.2f, want %.2f", tc.name, got, tc.want)
			}
		})
	}
}

// Verifies incomplete tasks outrank completed tasks with identical BM25 relevance via status multiplier.
func TestSearchStore_TaskStatusBoost(t *testing.T) {
	store := newTestSearchStore(t)
	now := time.Now()
	due := now.Add(24 * time.Hour)

	entries := []SearchEntry{
		{Source: "gsuite", SourceID: "done-task", ContentType: "task",
			Title: "Completed report task", Content: "Submit report to manager completed",
			Metadata: json.RawMessage(`{"task_id":"done","status":"completed"}`),
			Timestamp: &due},
		{Source: "gsuite", SourceID: "todo-task", ContentType: "task",
			Title: "Pending report task", Content: "Submit report to manager pending",
			Metadata: json.RawMessage(`{"task_id":"todo","status":"needsAction"}`),
			Timestamp: &due},
	}
	store.IndexEntries(entries)

	results, err := store.Search("report manager", 10, "", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Pending report task" {
		t.Errorf("expected needsAction task to rank first, got %q (scores: %.4f vs %.4f)",
			results[0].Title, results[0].Score, results[1].Score)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("needsAction score %.4f should exceed completed score %.4f",
			results[0].Score, results[1].Score)
	}
}

// ---------- After/Before date filters ----------

// Verifies the after/before filters narrow results to entries within the given timestamp range.
func TestSearchStore_AfterBeforeFilter(t *testing.T) {
	store := newTestSearchStore(t)

	t1, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	t2, _ := time.Parse(time.RFC3339, "2024-06-01T00:00:00Z")
	t3, _ := time.Parse(time.RFC3339, "2024-12-01T00:00:00Z")

	entries := []SearchEntry{
		{Source: "gsuite", SourceID: "early", ContentType: "note_content",
			Title: "Early meeting notes", Content: "budget planning meeting early", Timestamp: &t1},
		{Source: "gsuite", SourceID: "mid", ContentType: "note_content",
			Title: "Mid meeting notes", Content: "budget planning meeting mid", Timestamp: &t2},
		{Source: "gsuite", SourceID: "late", ContentType: "note_content",
			Title: "Late meeting notes", Content: "budget planning meeting late", Timestamp: &t3},
	}
	store.IndexEntries(entries)

	// after filter: only mid and late (early at 2024-01-01 excluded)
	results, err := store.Search("meeting", 10, "", "", "2024-03-01T00:00:00Z", "")
	if err != nil {
		t.Fatalf("Search after: %v", err)
	}
	for _, r := range results {
		if r.Title == "Early meeting notes" {
			t.Errorf("after filter should exclude early entry")
		}
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with after filter, got %d", len(results))
	}

	// before filter: only early and mid (late at 2024-12-01 excluded)
	results, err = store.Search("meeting", 10, "", "", "", "2024-09-01T00:00:00Z")
	if err != nil {
		t.Fatalf("Search before: %v", err)
	}
	for _, r := range results {
		if r.Title == "Late meeting notes" {
			t.Errorf("before filter should exclude late entry")
		}
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with before filter, got %d", len(results))
	}

	// combined after+before: only mid
	results, err = store.Search("meeting", 10, "", "", "2024-03-01T00:00:00Z", "2024-09-01T00:00:00Z")
	if err != nil {
		t.Fatalf("Search after+before: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Mid meeting notes" {
		t.Errorf("expected only mid entry, got %d results: %v", len(results), results)
	}
}
