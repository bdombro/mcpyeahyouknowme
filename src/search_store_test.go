package main

import (
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
	if got := searchMetadataHint("whatsapp", "message"); !strings.Contains(got, "message_id") {
		t.Fatalf("expected whatsapp message hint, got %q", got)
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
		{Source: "whatsapp", SourceID: "m4:group1@g.us", ContentType: "message", Title: "Family Chat", Content: "Family dinner tonight at seven", Metadata: json.RawMessage(`{"message_id":"m4","chat_jid":"group1@g.us","sender":"11111"}`)},
		{Source: "whatsapp", SourceID: "m7:group2@g.us", ContentType: "message", Title: "Work Team", Content: "Meeting at three pm tomorrow", Metadata: json.RawMessage(`{"message_id":"m7","chat_jid":"group2@g.us","sender":"33333"}`)},
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

// Verifies indexing computes one embedding per indexed entry when an embedder is present.
func TestSearchStore_IndexEntries_withEmbeddings(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)
	entries := seedSearchEntries()
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries with embedder: %v", err)
	}

	var embCount int
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&embCount)
	if embCount != len(entries) {
		t.Errorf("expected %d embeddings, got %d", len(entries), embCount)
	}
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
			if r.ContentType != "chat_name" && r.ContentType != "message" {
				t.Errorf("unexpected content_type: %s", r.ContentType)
			}
		}
	}
	if !found {
		t.Error("expected to find 'Family Chat' in results")
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

	results, err := store.Search("dinner", 10, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected hybrid results for 'dinner'")
	}
}

// Verifies hybrid search respects message-only filtering while still finding semantic matches.
func TestSearchStore_HybridSearch_messageOnly(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())

	results, err := store.Search("meeting", 10, "", "message")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ContentType != "message" {
			t.Errorf("expected only messages, got %s", r.ContentType)
		}
		if strings.Contains(strings.ToLower(r.Content), "meeting") {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'meeting' message")
	}
}

// Verifies hybrid search skips query embedding when BM25 already satisfies the requested limit.
func TestSearchStore_Search_skipsVectorWhenBM25Sufficient(t *testing.T) {
	emb := &countingEmbedder{base: &mockEmbedder{dim: 16}}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())

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

	if _, err := store.Search("tomorrow", 10, "", "message"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if emb.queryCalls == 0 {
		t.Fatal("expected vector search to run when BM25 results do not fill the limit")
	}
}

// ---------- Hierarchy Weighting ----------

// Verifies hierarchy weights lift chat names above weaker participant and message matches.
func TestSearchStore_HierarchyWeighting(t *testing.T) {
	emb := &mockEmbedder{dim: 16}
	store := newTestSearchStore(t, emb)

	now := time.Now()
	entries := []SearchEntry{
		{Source: "whatsapp", SourceID: "family-chat", ContentType: "chat_name", Title: "Family", Content: "Family", Timestamp: &now},
		{Source: "whatsapp", SourceID: "alice-family", ContentType: "participant", Title: "Family Alice", Content: "Family Alice", Timestamp: &now},
		{Source: "whatsapp", SourceID: "msg-family", ContentType: "message", Title: "Chat", Content: "Family dinner tonight", Timestamp: &now},
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
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

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
	// Second call: all entries already have embeddings → empty batch
	if err := store.IndexEntries(entries); err != nil {
		t.Fatalf("second IndexEntries: %v", err)
	}
}

// Verifies embedding failures propagate from indexing so the daemon can report broken model work.
func TestSearchStore_computeEmbeddings_embedError(t *testing.T) {
	store := newTestSearchStore(t, &errorEmbedder{})
	err := store.IndexEntries(seedSearchEntries())
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

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&count)
	if count != len(entries) {
		t.Errorf("expected %d embeddings, got %d", len(entries), count)
	}
}

// Verifies embedChunk reports completion once no missing embeddings remain.
func TestSearchStore_embedChunk_returnsZeroWhenDone(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	store := newTestSearchStore(t, emb)
	store.IndexEntries(seedSearchEntries())

	n, err := store.embedChunk(16, embeddingChunkSize)
	if err != nil {
		t.Fatalf("embedChunk: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 (all already embedded), got %d", n)
	}
}

// ---------- IndexStats ----------

// Verifies index stats report entry and embedding totals from the live store.
func TestSearchStore_IndexStats(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	store := newTestSearchStore(t, emb)
	entries := seedSearchEntries()
	store.IndexEntries(entries)

	stats := store.IndexStats()
	if stats.Entries != len(entries) {
		t.Errorf("Entries = %d, want %d", stats.Entries, len(entries))
	}
	if stats.Embedded != len(entries) {
		t.Errorf("Embedded = %d, want %d", stats.Embedded, len(entries))
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

	// Call vectorSearch with limit=2 directly so the len(all)>limit branch is exercised.
	results, err := store.vectorSearch("content", 2, "", "")
	if err != nil {
		t.Fatalf("vectorSearch: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(results))
	}
}
