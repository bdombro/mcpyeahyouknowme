package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// errorEmbedder always returns errors, for testing error-path coverage.
type errorEmbedder struct{}

// Returns an embedding error so search-store tests can cover failure paths without real model work.
func (e *errorEmbedder) EmbedTexts(_ []string, _ int) ([][]float32, error) {
	return nil, errors.New("embedTexts error")
}

// Returns an embedding error so vector-search tests can confirm graceful BM25 fallback.
func (e *errorEmbedder) EmbedQuery(_ string) ([]float32, error) {
	return nil, errors.New("embedQuery error")
}

// Releases nothing because the error embedder is only a test double.
func (e *errorEmbedder) Close() {}

// nilSlotEmbedder returns nil for the first entry, real embeddings for the rest.
type nilSlotEmbedder struct{ mock *mockEmbedder }

// Returns a nil first embedding so tests can verify empty-sentinel rows are stored and skipped correctly.
func (n *nilSlotEmbedder) EmbedTexts(texts []string, _ int) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if i == 0 {
			out[i] = nil
		} else {
			out[i] = n.mock.hashEmbed(t)
		}
	}
	return out, nil
}

// Verifies metadata-hint lookup returns guidance for known pairs and stays empty for unknown content.
func TestSearchMetadataHint_knownAndUnknown(t *testing.T) {
	if got := searchMetadataHint("whatsapp", "chat_content"); !strings.Contains(got, "start_message_id") {
		t.Fatalf("expected whatsapp chat-content hint, got %q", got)
	}
	if got := searchMetadataHint("unknown", "type"); got != "" {
		t.Fatalf("expected empty hint for unknown content, got %q", got)
	}
}

// Returns a deterministic query embedding so nil-slot tests can still exercise vector search.
func (n *nilSlotEmbedder) EmbedQuery(query string) ([]float32, error) {
	return n.mock.hashEmbed(query), nil
}

// Releases nothing because the nil-slot embedder wraps only in-memory test state.
func (n *nilSlotEmbedder) Close() {}

type countingEmbedder struct {
	base       EmbedderInterface
	queryCalls int
	textsCalls int
}

type batchRecordingEmbedder struct {
	base       EmbedderInterface
	batchSizes []int
}

type cancelingEmbedder struct {
	base      EmbedderInterface
	onTexts   func()
	textsSeen int
}

// Increments passage-call counts so tests can assert whether embedding work was skipped.
func (c *countingEmbedder) EmbedTexts(texts []string, batchSize int) ([][]float32, error) {
	c.textsCalls++
	return c.base.EmbedTexts(texts, batchSize)
}

// Increments query-call counts so tests can prove early-exit behavior in hybrid search.
func (c *countingEmbedder) EmbedQuery(query string) ([]float32, error) {
	c.queryCalls++
	return c.base.EmbedQuery(query)
}

// Releases the wrapped embedder so test doubles preserve production cleanup semantics.
func (c *countingEmbedder) Close() {
	c.base.Close()
}

// Records each batch size so tests can verify adaptive embedding logic changes size between chunks.
func (b *batchRecordingEmbedder) EmbedTexts(texts []string, batchSize int) ([][]float32, error) {
	b.batchSizes = append(b.batchSizes, batchSize)
	return b.base.EmbedTexts(texts, batchSize)
}

// Delegates query embeddings to the wrapped base embedder because only passage batch sizing matters in these tests.
func (b *batchRecordingEmbedder) EmbedQuery(query string) ([]float32, error) {
	return b.base.EmbedQuery(query)
}

// Releases the wrapped embedder so batch-recording tests preserve production cleanup semantics.
func (b *batchRecordingEmbedder) Close() {
	b.base.Close()
}

// Cancels the test context after the first batch so computeEmbeddings can cover its restart-friendly cancellation path.
func (c *cancelingEmbedder) EmbedTexts(texts []string, batchSize int) ([][]float32, error) {
	c.textsSeen++
	embeddings, err := c.base.EmbedTexts(texts, batchSize)
	if c.textsSeen == 1 && c.onTexts != nil {
		c.onTexts()
	}
	return embeddings, err
}

// Delegates query embeddings to the wrapped base embedder because only passage cancellation matters in these tests.
func (c *cancelingEmbedder) EmbedQuery(query string) ([]float32, error) {
	return c.base.EmbedQuery(query)
}

// Releases the wrapped embedder so cancellation tests preserve production cleanup semantics.
func (c *cancelingEmbedder) Close() {
	c.base.Close()
}

// mockEmbedder returns deterministic embeddings for testing.
type mockEmbedder struct {
	dim int
}

// Returns deterministic passage embeddings so hybrid search tests can run without ONNX.
func (m *mockEmbedder) EmbedTexts(texts []string, _ int) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.hashEmbed(t)
	}
	return out, nil
}

// Returns a deterministic query embedding so ranking assertions stay stable across runs.
func (m *mockEmbedder) EmbedQuery(query string) ([]float32, error) {
	return m.hashEmbed(query), nil
}

// Releases nothing because the mock embedder owns no external resources.
func (m *mockEmbedder) Close() {}

// Builds a normalized pseudo-embedding from text so search tests can compare semantic scoring deterministically.
func (m *mockEmbedder) hashEmbed(text string) []float32 {
	vec := make([]float32, m.dim)
	for i, c := range text {
		vec[(int(c)+i)%m.dim] += 1.0
	}
	// L2 normalize
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec
}

// Returns an initialized in-memory search store so tests can exercise indexing and querying without touching disk.
func newTestSearchStore(t *testing.T, embedder EmbedderInterface) *SearchStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open test search db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=5000")

	store, err := NewSearchStoreFromDB(db, embedder)
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

// Computes pending embeddings explicitly so tests mirror the daemon and reindex flows.
func computePendingEmbeddingsForTest(t *testing.T, store *SearchStore) {
	t.Helper()
	if err := store.ComputePendingEmbeddings(); err != nil {
		t.Fatalf("ComputePendingEmbeddings: %v", err)
	}
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
	store := newTestSearchStore(t, nil)
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
	store := newTestSearchStore(t, nil)
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
	store := newTestSearchStore(t, nil)
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

	results, err := store.Search("John Thomas", 10, "", "")
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
	store, err := NewSearchStoreFromDB(db, nil)
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

// Verifies PruneSource removes stale rows that no longer appear in a source's latest SearchEntries output.
func TestSearchStore_PruneSource_subset(t *testing.T) {
	store := newTestSearchStore(t, nil)
	entries := []SearchEntry{
		{Source: "notebook", SourceID: "note-1", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"},
		{Source: "notebook", SourceID: "note-1#chunk0", ContentType: "note_content", Title: "John Thomas", Content: "John Thomas has 2 kids"},
		{Source: "notebook", SourceID: "note-2", ContentType: "note_title", Title: "Old note", Content: "Old note"},
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	current := []SearchEntry{
		{Source: "notebook", SourceID: "note-1", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"},
		{Source: "notebook", SourceID: "note-1#chunk0", ContentType: "note_content", Title: "John Thomas", Content: "John Thomas has 2 kids"},
	}
	if err := store.PruneSource("notebook", current); err != nil {
		t.Fatalf("PruneSource: %v", err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'notebook'`).Scan(&count); err != nil {
		t.Fatalf("count notebook rows: %v", err)
	}
	if count != len(current) {
		t.Fatalf("expected %d notebook rows after prune, got %d", len(current), count)
	}
	results, err := store.Search("Old note", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected stale note to be pruned, got %#v", results)
	}
}

// Verifies PruneSource deletes every row for a source when that source returns no current SearchEntries.
func TestSearchStore_PruneSource_empty(t *testing.T) {
	store := newTestSearchStore(t, nil)
	entries := []SearchEntry{
		{Source: "notebook", SourceID: "note-1", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"},
		{Source: "notebook", SourceID: "note-1#chunk0", ContentType: "note_content", Title: "John Thomas", Content: "John Thomas has 2 kids"},
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	if err := store.PruneSource("notebook", nil); err != nil {
		t.Fatalf("PruneSource: %v", err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'notebook'`).Scan(&count); err != nil {
		t.Fatalf("count notebook rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected notebook rows to be deleted, got %d", count)
	}
}

// Verifies PruneSource returns an error when the DB handle is closed before prune bookkeeping can begin.
func TestSearchStore_PruneSource_closedDB(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	store, err := NewSearchStoreFromDB(db, nil)
	if err != nil {
		t.Fatalf("NewSearchStoreFromDB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := store.PruneSource("notebook", []SearchEntry{{Source: "notebook", SourceID: "note-1", ContentType: "note_title"}}); err == nil {
		t.Fatal("expected closed DB error")
	}
}

// Verifies indexing leaves embeddings pending until callers run the explicit embedding pass.
func TestSearchStore_IndexEntries_withEmbeddings(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)
	entries := seedSearchEntries()
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries with embedder: %v", err)
	}

	var embCount int
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&embCount)
	if embCount != 0 {
		t.Fatalf("expected 0 embeddings before explicit compute, got %d", embCount)
	}

	computePendingEmbeddingsForTest(t, store)
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&embCount)
	if embCount != len(entries) {
		t.Errorf("expected %d embeddings, got %d", len(entries), embCount)
	}
}

// Verifies the explicit embedding pass fills missing rows and becomes a no-op without an embedder.
func TestSearchStore_ComputePendingEmbeddings(t *testing.T) {
	t.Run("fills missing rows", func(t *testing.T) {
		store := newTestSearchStore(t, &mockEmbedder{dim: 16})
		entries := seedSearchEntries()
		if err := store.IndexEntries(entries); err != nil {
			t.Fatalf("IndexEntries: %v", err)
		}

		computePendingEmbeddingsForTest(t, store)

		var embCount int
		store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&embCount)
		if embCount != len(entries) {
			t.Fatalf("expected %d embeddings, got %d", len(entries), embCount)
		}
	})

	t.Run("no embedder is no-op", func(t *testing.T) {
		store := newTestSearchStore(t, nil)
		if err := store.IndexEntries(seedSearchEntries()); err != nil {
			t.Fatalf("IndexEntries: %v", err)
		}
		if err := store.ComputePendingEmbeddings(); err != nil {
			t.Fatalf("ComputePendingEmbeddings without embedder: %v", err)
		}

		var embCount int
		store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&embCount)
		if embCount != 0 {
			t.Fatalf("expected 0 embeddings without embedder, got %d", embCount)
		}
	})

	t.Run("canceled context stops early", func(t *testing.T) {
		store := newTestSearchStore(t, &mockEmbedder{dim: 16})
		if err := store.IndexEntries(seedSearchEntries()); err != nil {
			t.Fatalf("IndexEntries: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := store.ComputePendingEmbeddingsContext(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}

		var embCount int
		store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&embCount)
		if embCount != 0 {
			t.Fatalf("expected 0 embeddings after early cancellation, got %d", embCount)
		}
	})

	t.Run("nil context behaves like background", func(t *testing.T) {
		store := newTestSearchStore(t, &mockEmbedder{dim: 16})
		if err := store.IndexEntries(seedSearchEntries()); err != nil {
			t.Fatalf("IndexEntries: %v", err)
		}
		var nilCtx context.Context

		if err := store.computeEmbeddings(nilCtx); err != nil {
			t.Fatalf("computeEmbeddings with nil context: %v", err)
		}
	})
}

// ---------- BM25-only Search ----------

// Verifies BM25 search returns expected matches and content types for a simple keyword query.
func TestSearchStore_BM25Search(t *testing.T) {
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Family", 10, "", "")
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
	store := newTestSearchStore(t, nil)
	if err := store.IndexEntries([]SearchEntry{
		{Source: "notebook", SourceID: "john-thomas", ContentType: "note_content", Title: "John Thomas", Content: "Moved in 2022 for sake of 2 daughters"},
	}); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	results, err := store.Search("John Thomas daughters", 10, "", "")
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
		{name: "multi word", query: "John Thomas", want: `"John" "Thomas"`},
		{name: "special chars", query: "john@example.com", want: `"john" "example" "com"`},
		{name: "quotes and punctuation", query: `"John", Thomas!?`, want: `"John" "Thomas"`},
		{name: "empty", query: "", want: `""`},
		{name: "single word", query: "Family", want: `"Family"`},
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
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("zzzznonexistent", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// Verifies source filtering constrains BM25 results to the requested source.
func TestSearchStore_BM25Search_sourceFilter(t *testing.T) {
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("dinner", 10, "whatsapp", "")
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
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Family", 10, "", "chat_name")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.ContentType != "chat_name" {
			t.Errorf("expected content_type=chat_name, got %s", r.ContentType)
		}
	}
}

// ---------- Hybrid Search (BM25 + Vector) ----------

// Verifies hybrid search returns results when both BM25 and vector scoring are available.
func TestSearchStore_HybridSearch(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())
	computePendingEmbeddingsForTest(t, store)

	results, err := store.Search("dinner", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected hybrid results for 'dinner'")
	}
}

// Verifies hybrid search respects chat-content filtering while still finding semantic matches.
func TestSearchStore_HybridSearch_chatContentOnly(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())
	computePendingEmbeddingsForTest(t, store)

	results, err := store.Search("meeting", 10, "", "chat_content")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ContentType != "chat_content" {
			t.Errorf("expected only chat_content entries, got %s", r.ContentType)
		}
		if strings.Contains(strings.ToLower(r.Content), "meeting") {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'meeting' chat chunk")
	}
}

// Verifies hybrid search skips query embedding when BM25 already satisfies the requested limit.
func TestSearchStore_Search_skipsVectorWhenBM25Sufficient(t *testing.T) {
	emb := &countingEmbedder{base: &mockEmbedder{dim: 16}}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())
	computePendingEmbeddingsForTest(t, store)

	results, err := store.Search("Family", 1, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected BM25 results for 'Family'")
	}
	if emb.queryCalls != 0 {
		t.Fatalf("expected vector search to be skipped, got %d query embeddings", emb.queryCalls)
	}
}

// Verifies hybrid search still runs vector scoring when BM25 alone cannot fill the requested result set.
func TestSearchStore_Search_runsVectorWhenBM25Insufficient(t *testing.T) {
	emb := &countingEmbedder{base: &mockEmbedder{dim: 16}}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())
	computePendingEmbeddingsForTest(t, store)

	if _, err := store.Search("tomorrow", 10, "", "chat_content"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if emb.queryCalls == 0 {
		t.Fatal("expected vector search to run when BM25 results do not fill the limit")
	}
}

// ---------- Hierarchy Weighting ----------

// Verifies hierarchy weights lift chat names above weaker participant and chat-content matches.
func TestSearchStore_HierarchyWeighting(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)

	now := time.Now()
	entries := []SearchEntry{
		{Source: "whatsapp", SourceID: "family-chat", ContentType: "chat_name", Title: "Family", Content: "Family", Timestamp: &now},
		{Source: "whatsapp", SourceID: "alice-family", ContentType: "participant", Title: "Family Alice", Content: "Family Alice", Timestamp: &now},
		{Source: "whatsapp", SourceID: "family-chat#chunk:000", ContentType: "chat_content", Title: "Chat", Content: "Family dinner tonight", Timestamp: &now},
	}
	store.IndexEntries(entries)

	results, err := store.Search("Family", 10, "", "")
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

// ---------- RRF Fusion ----------

// Verifies RRF fusion preserves a single ranked list without dropping entries.
func TestRRFFuse_singleList(t *testing.T) {
	list := []rankedEntry{
		{entryID: 1, score: 10.0},
		{entryID: 2, score: 5.0},
	}
	fused := rrfFuse(list)
	if len(fused) != 2 {
		t.Fatalf("expected 2, got %d", len(fused))
	}
}

// Verifies RRF rewards entries that appear in multiple retrieval lists.
func TestRRFFuse_twoLists(t *testing.T) {
	list1 := []rankedEntry{
		{entryID: 1, score: 10.0},
		{entryID: 2, score: 5.0},
	}
	list2 := []rankedEntry{
		{entryID: 2, score: 8.0},
		{entryID: 3, score: 3.0},
	}
	fused := rrfFuse(list1, list2)
	if len(fused) != 3 {
		t.Fatalf("expected 3, got %d", len(fused))
	}
	// Entry 2 appears in both lists, should have highest RRF score
	scoreMap := make(map[int64]float64)
	for _, r := range fused {
		scoreMap[r.entryID] = r.score
	}
	if scoreMap[2] <= scoreMap[1] {
		t.Errorf("entry 2 (in both lists) should score higher than entry 1: %.4f vs %.4f", scoreMap[2], scoreMap[1])
	}
}

// Verifies RRF returns no results when every input list is empty.
func TestRRFFuse_empty(t *testing.T) {
	fused := rrfFuse(nil, nil)
	if len(fused) != 0 {
		t.Errorf("expected 0, got %d", len(fused))
	}
}

// ---------- Vector Math ----------

// Verifies cosine similarity returns one for identical vectors so ranking math is normalized.
func TestCosineSimilarity_identical(t *testing.T) {
	a := []float32{1, 0, 0}
	sim := cosineSimilarity(a, a)
	if math.Abs(sim-1.0) > 0.001 {
		t.Errorf("expected ~1.0, got %.4f", sim)
	}
}

// Verifies cosine similarity returns zero for orthogonal vectors.
func TestCosineSimilarity_orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := cosineSimilarity(a, b)
	if math.Abs(sim) > 0.001 {
		t.Errorf("expected ~0.0, got %.4f", sim)
	}
}

// Verifies cosine similarity returns zero for empty inputs instead of panicking.
func TestCosineSimilarity_empty(t *testing.T) {
	sim := cosineSimilarity(nil, nil)
	if sim != 0 {
		t.Errorf("expected 0, got %.4f", sim)
	}
}

// Verifies cosine similarity returns zero when vector lengths differ.
func TestCosineSimilarity_mismatchedLengths(t *testing.T) {
	sim := cosineSimilarity([]float32{1, 2}, []float32{1, 2, 3})
	if sim != 0 {
		t.Errorf("expected 0 for mismatched, got %.4f", sim)
	}
}

// Verifies float32 embedding blobs round-trip through SQLite byte packing without drift beyond tolerance.
func TestFloat32sRoundTrip(t *testing.T) {
	original := []float32{1.5, -2.3, 0, 100.001}
	bytes := float32sToBytes(original)
	restored := bytesToFloat32s(bytes)
	if len(restored) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(restored), len(original))
	}
	for i := range original {
		if math.Abs(float64(original[i]-restored[i])) > 0.0001 {
			t.Errorf("index %d: %.4f != %.4f", i, original[i], restored[i])
		}
	}
}

// Verifies malformed embedding blobs return nil instead of partial float slices.
func TestBytesToFloat32s_badLength(t *testing.T) {
	result := bytesToFloat32s([]byte{1, 2, 3}) // not divisible by 4
	if result != nil {
		t.Errorf("expected nil for bad length, got %v", result)
	}
}

// ---------- Source Timestamp Tracking ----------

// Verifies per-source last-indexed timestamps can be written and read back accurately.
func TestSearchStore_SourceTimestamp(t *testing.T) {
	store := newTestSearchStore(t, nil)
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
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Family", 0, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// limit=0 should default to 20, which is more than our entries
	if len(results) == 0 {
		t.Error("expected results with default limit")
	}
}

// ---------- Graceful Degradation ----------

// Verifies BM25 search still works when no embedder is configured.
func TestSearchStore_NilEmbedder_BM25Only(t *testing.T) {
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("dinner", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected BM25 results even without embedder")
	}
}

// ---------- Metadata in results ----------

// Verifies search results expose parsed metadata needed for follow-up tool calls.
func TestSearchStore_ResultMetadata(t *testing.T) {
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("Alice", 10, "", "participant")
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

	store, err := NewSearchStore(tmpDir, nil)
	if err != nil {
		t.Fatalf("NewSearchStore: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// Verify database file was created
	dbPath := filepath.Join(tmpDir, "search.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("database file not created at %s", dbPath)
	}

	// Verify we can use it
	entries := seedSearchEntries()
	if err := store.IndexEntries(entries[:1]); err != nil {
		t.Errorf("IndexEntries: %v", err)
	}

	store.Close()
}

// ---------- Close ----------

// Verifies Close shuts down the underlying database connection and future queries fail.
func TestSearchStore_Close(t *testing.T) {
	store := newTestSearchStore(t, nil)

	err := store.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	// Verify database is closed
	_, err = store.db.Query("SELECT 1")
	if err == nil {
		t.Error("expected error querying closed db")
	}
}

// ---------- Edge cases ----------

// Verifies entries with empty title and content still persist without crashing indexing.
func TestSearchStore_IndexEntries_emptyContent(t *testing.T) {
	store := newTestSearchStore(t, nil)

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

// ---------- rebuildFTSIfNeeded empty store ----------

// Verifies indexing an empty slice is a no-op rather than an error.
func TestSearchStore_IndexEntries_emptySlice(t *testing.T) {
	store := newTestSearchStore(t, nil)
	if err := store.IndexEntries([]SearchEntry{}); err != nil {
		t.Fatalf("IndexEntries empty slice: %v", err)
	}
}

// Verifies bulk indexing defers FTS row maintenance until one final rebuild, then restores live trigger updates.
func TestSearchStore_BulkIndex_rebuildsFTSAtEnd(t *testing.T) {
	store := newTestSearchStore(t, nil)
	entries := seedSearchEntries()
	if err := store.BeginBulkIndex(); err != nil {
		t.Fatalf("BeginBulkIndex: %v", err)
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	beforeResults, err := store.Search("Family", 10, "", "")
	if err != nil {
		t.Fatalf("Search before EndBulkIndex: %v", err)
	}
	if len(beforeResults) != 0 {
		t.Fatalf("expected deferred FTS writes during bulk load, got %d search hits", len(beforeResults))
	}

	if err := store.EndBulkIndex(); err != nil {
		t.Fatalf("EndBulkIndex: %v", err)
	}

	afterResults, err := store.Search("Family", 10, "", "")
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

	finalResults, err := store.Search("after", 10, "", "")
	if err != nil {
		t.Fatalf("Search after restored triggers: %v", err)
	}
	if len(finalResults) != 1 || finalResults[0].Title != "after" {
		t.Fatalf("expected restored triggers to index post-bulk writes, got %+v", finalResults)
	}
}

// Verifies repeated BeginBulkIndex calls are harmless once FTS maintenance is already suspended.
func TestSearchStore_BeginBulkIndex_noopWhenAlreadyActive(t *testing.T) {
	store := newTestSearchStore(t, nil)
	if err := store.BeginBulkIndex(); err != nil {
		t.Fatalf("first BeginBulkIndex: %v", err)
	}
	if err := store.BeginBulkIndex(); err != nil {
		t.Fatalf("second BeginBulkIndex: %v", err)
	}
}

// Verifies EndBulkIndex is a no-op when bulk FTS mode was never enabled.
func TestSearchStore_EndBulkIndex_noopWhenInactive(t *testing.T) {
	store := newTestSearchStore(t, nil)
	if err := store.EndBulkIndex(); err != nil {
		t.Fatalf("EndBulkIndex inactive: %v", err)
	}
}

// Verifies bulk-index trigger helpers report actionable errors when the search DB handle is unavailable.
func TestSearchStore_BulkIndex_closedDBErrors(t *testing.T) {
	store := newTestSearchStore(t, nil)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := store.BeginBulkIndex(); err == nil {
		t.Fatal("expected BeginBulkIndex to fail on closed DB")
	}

	store = newTestSearchStore(t, nil)
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
	store := newTestSearchStore(t, nil)
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

// ---------- computeEmbeddings edge cases ----------

// Verifies whitespace-only entries are skipped when computing embeddings for missing rows.
func TestSearchStore_computeEmbeddings_skipEmptyText(t *testing.T) {
	store := newTestSearchStore(t, nil)
	entries := []SearchEntry{
		{Source: "test", SourceID: "ws1", ContentType: "message", Title: "  ", Content: ""},
		{Source: "test", SourceID: "real1", ContentType: "message", Title: "Real", Content: "content"},
	}
	store.IndexEntries(entries)

	store.embedder = &mockEmbedder{dim: 16}
	computePendingEmbeddingsForTest(t, store)

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 embedding (whitespace entry skipped), got %d", count)
	}
}

// Verifies repeated indexing stops cleanly once every entry already has an embedding row.
func TestSearchStore_computeEmbeddings_emptyBatch(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)
	entries := seedSearchEntries()

	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("first IndexEntries: %v", err)
	}
	computePendingEmbeddingsForTest(t, store)

	// Second call: all entries already have embeddings -> empty batch.
	if err := store.ComputePendingEmbeddings(); err != nil {
		t.Fatalf("second ComputePendingEmbeddings: %v", err)
	}
}

// Verifies embedding failures propagate from the explicit embedding pass so callers can report broken model work.
func TestSearchStore_computeEmbeddings_embedError(t *testing.T) {
	store := newTestSearchStore(t, &errorEmbedder{})
	if err := store.IndexEntries(seedSearchEntries()); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}
	err := store.ComputePendingEmbeddings()
	if err == nil {
		t.Error("expected error from errorEmbedder")
	}
}

// Verifies nil embeddings are stored as empty sentinels so the row is not retried forever.
func TestSearchStore_computeEmbeddings_nilEmbedding(t *testing.T) {
	emb := &nilSlotEmbedder{mock: &mockEmbedder{dim: 16}}
	store := newTestSearchStore(t, emb)
	entries := seedSearchEntries()

	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}
	computePendingEmbeddingsForTest(t, store)

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&count)
	expected := len(entries)
	if count != expected {
		t.Errorf("expected %d embedding rows including empty sentinel, got %d", expected, count)
	}

	var emptyCount int
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings WHERE length(embedding) = 0").Scan(&emptyCount)
	if emptyCount != 1 {
		t.Errorf("expected 1 empty sentinel embedding row, got %d", emptyCount)
	}
}

// Verifies vector search ignores empty-sentinel embeddings and still returns valid BM25 results.
func TestSearchStore_Search_skipsEmptyEmbeddingSentinel(t *testing.T) {
	emb := &nilSlotEmbedder{mock: &mockEmbedder{dim: 16}}
	store := newTestSearchStore(t, emb)
	if err := store.IndexEntries(seedSearchEntries()); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}
	computePendingEmbeddingsForTest(t, store)

	results, err := store.Search("Family", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results with empty sentinel embedding present")
	}
}

// ---------- Search with vector error ----------

// Verifies vector-query errors degrade to BM25 results instead of failing the full search request.
func TestSearchStore_Search_vectorSearchError(t *testing.T) {
	store := newTestSearchStore(t, nil)
	store.IndexEntries(seedSearchEntries())

	store.embedder = &errorEmbedder{}
	results, err := store.Search("Family", 10, "", "")
	if err != nil {
		t.Fatalf("Search should succeed despite vector error: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected BM25 results even when vector search fails")
	}
}

// ---------- loadResults unknown content type ----------

// Verifies unknown content types fall back to neutral weighting instead of zeroing scores.
func TestSearchStore_loadResults_unknownContentType(t *testing.T) {
	store := newTestSearchStore(t, nil)
	entries := []SearchEntry{
		{Source: "test", SourceID: "u1", ContentType: "unknown_type", Title: "Unique Test", Content: "unique test content"},
	}
	store.IndexEntries(entries)

	results, err := store.Search("unique", 10, "", "")
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

// ---------- cosineSimilarity zero vector ----------

// Verifies zero vectors return zero similarity instead of NaN after the SIMD swap.
func TestCosineSimilarity_zeroVector(t *testing.T) {
	a := []float32{0, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for zero vector, got %f", sim)
	}
}

// ---------- Chunked embeddings ----------

// Verifies large embedding jobs commit successfully across multiple chunks.
func TestSearchStore_chunkedEmbeddings_commitsPerChunk(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	store := newTestSearchStore(t, emb)

	entries := make([]SearchEntry, embeddingChunkSize*2+25)
	for i := range entries {
		entries[i] = SearchEntry{
			Source: "test", SourceID: fmt.Sprintf("chunk%d", i),
			ContentType: "message", Title: fmt.Sprintf("doc %d", i),
			Content: fmt.Sprintf("content number %d for chunked test", i),
		}
	}
	store.IndexEntries(entries)
	computePendingEmbeddingsForTest(t, store)

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&count)
	if count != len(entries) {
		t.Errorf("expected %d embeddings, got %d", len(entries), count)
	}
}

// Verifies computeEmbeddings exits with context cancellation between embedding batches so daemon restarts can resume quickly.
func TestSearchStore_computeEmbeddings_contextCanceledBetweenBatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	emb := &cancelingEmbedder{
		base: &mockEmbedder{dim: 8},
		onTexts: func() {
			cancel()
		},
	}
	store := newTestSearchStore(t, emb)

	entries := make([]SearchEntry, embeddingChunkSize+25)
	for i := range entries {
		entries[i] = SearchEntry{
			Source: "test", SourceID: fmt.Sprintf("cancel%d", i),
			ContentType: "message", Title: fmt.Sprintf("doc %d", i),
			Content: fmt.Sprintf("content number %d for cancellation test", i),
		}
	}
	store.IndexEntries(entries)

	err := store.computeEmbeddings(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

// Verifies computeEmbeddings re-samples memory each chunk and only downshifts the active batch size mid-run.
func TestSearchStore_computeEmbeddings_downshiftsBatchSizePerChunk(t *testing.T) {
	originalBatchSizer := searchStoreAdaptiveBatchSize
	defer func() {
		searchStoreAdaptiveBatchSize = originalBatchSizer
	}()

	sampled := []int{64, 16, 128}
	calls := 0
	searchStoreAdaptiveBatchSize = func() int {
		if calls >= len(sampled) {
			return sampled[len(sampled)-1]
		}
		size := sampled[calls]
		calls++
		return size
	}

	emb := &batchRecordingEmbedder{base: &mockEmbedder{dim: 8}}
	store := newTestSearchStore(t, emb)

	entries := make([]SearchEntry, embeddingChunkSize+5)
	for i := range entries {
		entries[i] = SearchEntry{
			Source: "test", SourceID: fmt.Sprintf("downshift%d", i),
			ContentType: "message", Title: fmt.Sprintf("doc %d", i),
			Content: fmt.Sprintf("content number %d for batch downshift test", i),
		}
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	if err := store.computeEmbeddings(context.Background()); err != nil {
		t.Fatalf("computeEmbeddings: %v", err)
	}

	if len(emb.batchSizes) != 2 {
		t.Fatalf("expected 2 embedding batches, got %d", len(emb.batchSizes))
	}
	if emb.batchSizes[0] != 64 || emb.batchSizes[1] != 16 {
		t.Fatalf("expected batch sizes [64 16], got %v", emb.batchSizes)
	}
}

// Verifies batch-size sampling initializes, refuses to upshift mid-run, and clamps invalid samples to 1.
func TestNextEmbeddingBatchSize(t *testing.T) {
	originalBatchSizer := searchStoreAdaptiveBatchSize
	defer func() {
		searchStoreAdaptiveBatchSize = originalBatchSizer
	}()

	tests := []struct {
		name    string
		current int
		sampled int
		want    int
	}{
		{name: "initializes from first sample", current: 0, sampled: 64, want: 64},
		{name: "downshifts when memory drops", current: 64, sampled: 16, want: 16},
		{name: "does not upshift mid run", current: 16, sampled: 128, want: 16},
		{name: "clamps invalid sample", current: 16, sampled: 0, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			searchStoreAdaptiveBatchSize = func() int { return tt.sampled }
			if got := nextEmbeddingBatchSize(tt.current); got != tt.want {
				t.Fatalf("nextEmbeddingBatchSize(%d) = %d, want %d", tt.current, got, tt.want)
			}
		})
	}
}

// Verifies computeEmbeddings returns immediately when the context is already canceled before any batch work starts.
func TestSearchStore_computeEmbeddings_preCanceledContext(t *testing.T) {
	store := newTestSearchStore(t, &mockEmbedder{dim: 8})
	if err := store.IndexEntries(seedSearchEntries()); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := store.computeEmbeddings(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

// Verifies embedChunk reports completion once no missing embeddings remain.
func TestSearchStore_embedChunk_returnsZeroWhenDone(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())
	computePendingEmbeddingsForTest(t, store)

	_, n, err := store.embedChunk(0, 16, embeddingChunkSize)
	if err != nil {
		t.Fatalf("embedChunk: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 (all already embedded), got %d", n)
	}
}

// Verifies embedChunk advances past whitespace-only rows without rescanning them forever before embedding later rows.
func TestSearchStore_embedChunk_skipsWhitespacePageAndAdvancesCursor(t *testing.T) {
	store := newTestSearchStore(t, &mockEmbedder{dim: 8})
	entries := []SearchEntry{
		{Source: "test", SourceID: "blank-1", ContentType: "message", Title: "", Content: "   "},
		{Source: "test", SourceID: "blank-2", ContentType: "message", Title: "", Content: "\n\t"},
		{Source: "test", SourceID: "real-1", ContentType: "message", Title: "real", Content: "content"},
	}
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	nextID, n, err := store.embedChunk(0, 16, 2)
	if err != nil {
		t.Fatalf("embedChunk: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 embedded row after skipping whitespace page, got %d", n)
	}

	var embeddedCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&embeddedCount); err != nil {
		t.Fatalf("count embeddings: %v", err)
	}
	if embeddedCount != 1 {
		t.Fatalf("expected 1 stored embedding, got %d", embeddedCount)
	}
	if nextID <= 2 {
		t.Fatalf("expected cursor to advance past blank rows, got %d", nextID)
	}
}

// ---------- IndexStats ----------

// Verifies index stats report entry and embedding totals from the live store.
func TestSearchStore_IndexStats(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	store := newTestSearchStore(t, emb)
	entries := seedSearchEntries()
	store.IndexEntries(entries)
	computePendingEmbeddingsForTest(t, store)

	stats := store.IndexStats()
	if stats.Entries != len(entries) {
		t.Errorf("Entries = %d, want %d", stats.Entries, len(entries))
	}
	if stats.Embedded != len(entries) {
		t.Errorf("Embedded = %d, want %d", stats.Embedded, len(entries))
	}
}

// Verifies Clear removes indexed rows, embedding rows, and source timestamps so full rebuilds restart from zero.
func TestSearchStore_Clear(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	store := newTestSearchStore(t, emb)
	entries := seedSearchEntries()
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}
	computePendingEmbeddingsForTest(t, store)
	store.UpdateSourceTimestamp("test", time.Now())

	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	stats := store.IndexStats()
	if stats.Entries != 0 || stats.Embedded != 0 {
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
	store := newTestSearchStore(t, nil)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := store.Clear()
	if err == nil || !strings.Contains(err.Error(), "clear search index") {
		t.Fatalf("Clear error = %v", err)
	}
}

// Verifies read-only stats return zeros when no search database exists yet.
func TestReadOnlySearchIndexStats_noDB(t *testing.T) {
	stats := ReadOnlySearchIndexStats(t.TempDir())
	if stats.Entries != 0 {
		t.Errorf("expected 0 entries for non-existent DB, got %d", stats.Entries)
	}
}

// Verifies read-only stats can inspect a populated on-disk search database without an embedder.
func TestReadOnlySearchIndexStats_withDB(t *testing.T) {
	dir := t.TempDir()
	emb := &mockEmbedder{dim: 8}
	store, err := NewSearchStore(dir, emb)
	if err != nil {
		t.Fatalf("NewSearchStore: %v", err)
	}
	store.IndexEntries(seedSearchEntries())
	computePendingEmbeddingsForTest(t, store)
	store.Close()

	stats := ReadOnlySearchIndexStats(dir)
	if stats.Entries == 0 {
		t.Error("expected non-zero entries")
	}
	if stats.Embedded == 0 {
		t.Error("expected non-zero embedded")
	}
}

// Verifies vectorSearch truncates its ranked result list to the requested limit.
func TestSearchStore_VectorSearch_LimitTruncates(t *testing.T) {
	emb := &mockEmbedder{dim: 4}
	store := newTestSearchStore(t, emb)

	// Index more entries than the search limit so the truncation branch is hit.
	entries := make([]SearchEntry, 5)
	for i := range entries {
		entries[i] = SearchEntry{
			Source: "test", SourceID: fmt.Sprintf("id%d", i),
			ContentType: "message", Title: fmt.Sprintf("doc %d", i),
			Content: fmt.Sprintf("content number %d", i),
		}
	}
	store.IndexEntries(entries)
	computePendingEmbeddingsForTest(t, store)

	// Call vectorSearch with limit=2 directly so the len(all)>limit branch is exercised.
	results, err := store.vectorSearch("content", 2, "", "")
	if err != nil {
		t.Fatalf("vectorSearch: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(results))
	}
}
