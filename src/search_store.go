package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"mcpyeahyouknowme/core"

	"github.com/viterin/vek/vek32"
)

// SearchEntry is an alias for core.SearchEntry for backward compatibility.
type SearchEntry = core.SearchEntry

// SearchResult is returned by the global search MCP tool.
type SearchResult struct {
	Source       string          `json:"source"`
	ContentType  string          `json:"content_type"`
	Title        string          `json:"title"`
	Content      string          `json:"content"`
	Score        float64         `json:"score"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	MetadataHint string          `json:"metadata_hint,omitempty"`
}

// Hierarchy weights: name matches are most valuable, then participants, then content.
var hierarchyWeights = map[string]float64{
	// WhatsApp
	"chat_name":    3.0,
	"participant":  2.0,
	"chat_content": 1.0,
	// Google Docs
	"document_title":   2.0,
	"document_owner":   2.0,
	"document_content": 1.0,
	// Google Sheets
	"spreadsheet_title":   2.0,
	"spreadsheet_owner":   2.0,
	"spreadsheet_content": 1.0,
	// Gmail
	"email_subject":             2.5,
	"email_content":             1.0,
	"email_thread_subject":      2.5,
	"email_thread_participants": 2.0,
	"email_thread_content":      1.0,
	// Calendar
	"calendar_event":             2.0,
	"calendar_event_description": 1.0,
	// Tasks
	"task": 1.5,
	// Contacts
	"contact": 2.0,
	// Slides
	"presentation_title":   2.0,
	"presentation_owner":   2.0,
	"presentation_content": 1.0,
	// Notebook
	"note_title":   2.0,
	"note_content": 1.0,
	"pdf_title":    2.0,
	"pdf_content":  1.0,
	"image":        1.5,
	// Browser History
	"browser_visit": 1.8,
}

var searchMetadataHints = map[string]string{
	"whatsapp:chat_name":                `metadata contains {"jid","is_group"}`,
	"whatsapp:participant":              `metadata contains {"jid","groups"}; use jid with whatsapp_get_chat`,
	"whatsapp:chat_content":             `metadata contains {"chat_jid","chunk_index","start_message_id","end_message_id","start_timestamp","end_timestamp"}; use start_message_id with whatsapp_get_message_context`,
	"gsuite:document_title":             `metadata contains {"document_id","modified_time"}; use document_id with gsuite_docs_get_document`,
	"gsuite:document_owner":             `metadata contains {"document_id","modified_time"}; use document_id with gsuite_docs_get_document`,
	"gsuite:document_content":           `metadata contains {"document_id","modified_time"}; use document_id with gsuite_docs_get_document`,
	"gsuite:spreadsheet_title":          `metadata contains {"spreadsheet_id","modified_time"}; use spreadsheet_id with gsuite_sheets_get_spreadsheet`,
	"gsuite:spreadsheet_owner":          `metadata contains {"spreadsheet_id","modified_time"}; use spreadsheet_id with gsuite_sheets_get_spreadsheet`,
	"gsuite:spreadsheet_content":        `metadata contains {"spreadsheet_id","modified_time"}; use spreadsheet_id with gsuite_sheets_get_spreadsheet`,
	"gsuite:email_thread_subject":       `metadata contains {"thread_id","participants","last_date"}; use thread_id with gsuite_gmail_get_thread`,
	"gsuite:email_thread_participants":  `metadata contains {"thread_id","participants","last_date"}; use thread_id with gsuite_gmail_get_thread`,
	"gsuite:email_thread_content":       `metadata contains {"thread_id","participants","last_date"}; use thread_id with gsuite_gmail_get_thread`,
	"gsuite:email_subject":              `metadata contains {"message_id","from","date","folder"}; use message_id with gsuite_gmail_get_message`,
	"gsuite:email_content":              `metadata contains {"message_id","from","date","folder"}; use message_id with gsuite_gmail_get_message`,
	"gsuite:calendar_event":             `metadata contains {"event_id","start_time","end_time"}; use event_id with gsuite_calendar_get_event`,
	"gsuite:calendar_event_description": `metadata contains {"event_id","start_time","end_time"}; use event_id with gsuite_calendar_get_event`,
	"gsuite:task":                       `metadata contains {"task_id","status","due"}`,
	"gsuite:contact":                    `metadata contains {"resource_name","emails","phones"}`,
	"gsuite:presentation_title":         `metadata contains {"presentation_id","modified_time"}; use presentation_id with gsuite_slides_get_presentation`,
	"gsuite:presentation_owner":         `metadata contains {"presentation_id","modified_time"}; use presentation_id with gsuite_slides_get_presentation`,
	"gsuite:presentation_content":       `metadata contains {"presentation_id","modified_time"}; use presentation_id with gsuite_slides_get_presentation`,
	"notebook:note_title":               `metadata contains {"path","dir"}; use path with notebook_read`,
	"notebook:note_content":             `metadata contains {"path","dir","chunk"}; use path with notebook_read`,
	"notebook:pdf_title":                `metadata contains {"path","dir"}; use path with notebook_read_pdf`,
	"notebook:pdf_content":              `metadata contains {"path","dir","chunk"}; use path with notebook_read_pdf`,
	"notebook:image":                    `metadata contains {"path","dir","labels"}; use path with notebook_get_image`,
	"browser_history:browser_visit":     `metadata contains {"url","visit_count","last_visit_time","url_id","domain"}; use browser_history_search for visit rows`,
}

const rrfK = 60 // constant for Reciprocal Rank Fusion

// EmbedderInterface abstracts embedding operations for testability.
type EmbedderInterface interface {
	EmbedTexts(texts []string, batchSize int) ([][]float32, error)
	EmbedQuery(query string) ([]float32, error)
	Close()
}

// SearchStore manages the combined search index across all data sources.
type SearchStore struct {
	db       *sql.DB
	embedder EmbedderInterface
}

// Close releases the search DB handle so daemon, MCP, or CLI callers do not leave SQLite connections open.
func (s *SearchStore) Close() error {
	return s.db.Close()
}

// IndexEntries upserts entries into the search index so all sources become
// searchable before any background embedding work begins.
func (s *SearchStore) IndexEntries(entries []SearchEntry) error {
	tx, err := s.db.Begin()
	if err != nil { // nocov
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO search_entries (source, source_id, content_type, title, content, metadata, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source, source_id, content_type) DO UPDATE SET
			title=excluded.title, content=excluded.content,
			metadata=excluded.metadata, timestamp=excluded.timestamp`)
	if err != nil { // nocov
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, e := range entries {
		var ts interface{}
		if e.Timestamp != nil {
			ts = e.Timestamp.Format(time.RFC3339)
		}
		var meta interface{}
		if len(e.Metadata) > 0 {
			meta = string(e.Metadata)
		}
		if _, err := stmt.Exec(e.Source, e.SourceID, e.ContentType, e.Title, e.Content, meta, ts); err != nil { // nocov
			return fmt.Errorf("upsert entry %s/%s: %w", e.Source, e.SourceID, err)
		}
	}

	if err := tx.Commit(); err != nil { // nocov
		return err
	}

	// Rebuild FTS if needed (for the initial bulk load case)
	s.rebuildFTSIfNeeded()
	return nil
}

// rebuildFTSIfNeeded rebuilds the FTS index after bulk loads when entries exist but no FTS rows were created.
func (s *SearchStore) rebuildFTSIfNeeded() {
	var entryCount int
	s.db.QueryRow("SELECT COUNT(*) FROM search_entries").Scan(&entryCount)
	if entryCount == 0 {
		return
	}
	var indexed int
	s.db.QueryRow("SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH '*'").Scan(&indexed)
	if indexed == 0 {
		s.db.Exec("INSERT INTO search_fts(search_fts) VALUES('rebuild')")
	}
}

const embeddingChunkSize = 200

// computeEmbeddings generates embeddings for entries that don't have one yet,
// processing in chunks with per-chunk commits and adaptive batch sizing.
func (s *SearchStore) computeEmbeddings(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	batchSize := adaptiveBatchSize()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.embedChunk(batchSize, embeddingChunkSize)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// ComputePendingEmbeddings fills in missing embeddings after indexing so
// source insertion can finish before expensive model work starts.
func (s *SearchStore) ComputePendingEmbeddings() error {
	return s.ComputePendingEmbeddingsContext(context.Background())
}

// Fills in missing embeddings until completion or context cancellation so daemon reindex requests can restart promptly.
func (s *SearchStore) ComputePendingEmbeddingsContext(ctx context.Context) error {
	if s.embedder == nil {
		return nil
	}
	return s.computeEmbeddings(ctx)
}

type pendingEmbed struct {
	id   int64
	text string
}

// embedChunk processes the next page of entries missing embeddings.
// Returns the number of entries found (0 means done).
func (s *SearchStore) embedChunk(batchSize, limit int) (int, error) {
	rows, err := s.db.Query(`
		SELECT e.id, e.title, e.content
		FROM search_entries e
		LEFT JOIN search_embeddings se ON e.id = se.entry_id
		WHERE se.entry_id IS NULL
		ORDER BY e.id
		LIMIT ?`, limit)
	if err != nil { // nocov
		return 0, err
	}
	defer rows.Close()

	var chunk []pendingEmbed
	for rows.Next() {
		var id int64
		var title, content string
		if rows.Scan(&id, &title, &content) != nil { // nocov
			continue
		}
		text := title
		if content != "" {
			if text != "" {
				text += ": "
			}
			text += content
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		chunk = append(chunk, pendingEmbed{id: id, text: text})
	}

	if len(chunk) == 0 {
		return 0, nil
	}

	texts := make([]string, len(chunk))
	for i, p := range chunk {
		texts[i] = p.text
	}

	embeddings, err := s.embedder.EmbedTexts(texts, batchSize)
	if err != nil {
		return 0, fmt.Errorf("embed texts: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil { // nocov
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO search_embeddings (entry_id, embedding) VALUES (?, ?)")
	if err != nil { // nocov
		return 0, err
	}
	defer stmt.Close()

	for i, emb := range embeddings {
		blob := []byte{}
		if len(emb) > 0 {
			blob = float32sToBytes(emb)
		}
		if _, err := stmt.Exec(chunk[i].id, blob); err != nil { // nocov
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil { // nocov
		return 0, err
	}
	return len(chunk), nil
}

// UpdateSourceTimestamp fire-and-forgets the source's latest successful index time into search_meta for incremental reindex decisions.
func (s *SearchStore) UpdateSourceTimestamp(source string, t time.Time) {
	s.db.Exec(`INSERT INTO search_meta (source, last_indexed) VALUES (?, ?)
		ON CONFLICT(source) DO UPDATE SET last_indexed=excluded.last_indexed`,
		source, t.Format(time.RFC3339))
}

// LastIndexed reads the stored index watermark for source, returning zero time when incremental indexing has never recorded one.
func (s *SearchStore) LastIndexed(source string) time.Time {
	var ts sql.NullString
	s.db.QueryRow("SELECT last_indexed FROM search_meta WHERE source = ?", source).Scan(&ts)
	if ts.Valid {
		t, _ := time.Parse(time.RFC3339, ts.String)
		return t
	}
	return time.Time{}
}

// Search runs hybrid BM25+vector retrieval for `query`, falling back to BM25-only if query embedding fails, then returns weighted top results.
func (s *SearchStore) Search(query string, limit int, sourceFilter, typeFilter string) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	bm25Results := s.bm25SearchEntries(query, limit*5, sourceFilter, typeFilter)

	var vectorResults []rankedEntry
	if s.embedder != nil && len(bm25Results) < limit {
		var err error
		vectorResults, err = s.vectorSearch(query, limit*5, sourceFilter, typeFilter)
		if err != nil {
			vectorResults = nil
		}
	}

	fused := rrfFuse(bm25Results, vectorResults)

	sort.Slice(fused, func(i, j int) bool { return fused[i].score > fused[j].score })

	if len(fused) > limit {
		fused = fused[:limit]
	}

	return s.loadResults(fused)
}

type rankedEntry struct {
	entryID int64
	score   float64
}

// bm25SearchEntries runs FTS keyword search for query, applies optional filters, and returns ranked entry IDs.
func (s *SearchStore) bm25SearchEntries(query string, limit int, sourceFilter, typeFilter string) []rankedEntry {
	ftsQuery := sanitizeFTSQuery(query)

	parts := []string{`
		SELECT e.id, bm25(search_fts) as score
		FROM search_fts
		JOIN search_entries e ON search_fts.rowid = e.id
		WHERE search_fts MATCH ?`}
	params := []interface{}{ftsQuery}

	if sourceFilter != "" {
		parts = append(parts, "AND e.source = ?")
		params = append(params, sourceFilter)
	}
	if typeFilter != "" {
		parts = append(parts, "AND e.content_type = ?")
		params = append(params, typeFilter)
	}
	parts = append(parts, "ORDER BY bm25(search_fts) LIMIT ?")
	params = append(params, limit)

	rows, err := s.db.Query(strings.Join(parts, " "), params...)
	if err != nil { // nocov
		return nil
	}
	defer rows.Close()

	var results []rankedEntry
	for rows.Next() {
		var r rankedEntry
		if rows.Scan(&r.entryID, &r.score) == nil {
			results = append(results, r)
		}
	}
	return results
}

// sanitizeFTSQuery tokenizes natural-language queries into quoted FTS terms so
// keyword search matches per-word intent instead of requiring one exact phrase.
func sanitizeFTSQuery(query string) string {
	tokens := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(tokens) == 0 {
		safeQuery := strings.ReplaceAll(strings.TrimSpace(query), `"`, `""`)
		return `"` + safeQuery + `"`
	}

	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		safeToken := strings.ReplaceAll(token, `"`, `""`)
		quoted = append(quoted, `"`+safeToken+`"`)
	}
	return strings.Join(quoted, " ")
}

// vectorSearch embeds query, scores stored embeddings with cosine similarity, and returns top ranked entry IDs.
func (s *SearchStore) vectorSearch(query string, limit int, sourceFilter, typeFilter string) ([]rankedEntry, error) {
	queryEmb, err := s.embedder.EmbedQuery(query)
	if err != nil {
		return nil, err
	}

	filterParts := []string{"1=1"}
	var filterParams []interface{}
	if sourceFilter != "" {
		filterParts = append(filterParts, "e.source = ?")
		filterParams = append(filterParams, sourceFilter)
	}
	if typeFilter != "" {
		filterParts = append(filterParts, "e.content_type = ?")
		filterParams = append(filterParams, typeFilter)
	}

	rows, err := s.db.Query(
		"SELECT se.entry_id, se.embedding FROM search_embeddings se "+
			"JOIN search_entries e ON se.entry_id = e.id WHERE "+
			strings.Join(filterParts, " AND "),
		filterParams...)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		id    int64
		score float64
	}
	var all []scored
	for rows.Next() {
		var id int64
		var blob []byte
		if rows.Scan(&id, &blob) != nil { // nocov
			continue
		}
		if len(blob) == 0 {
			continue
		}
		emb := bytesToFloat32s(blob)
		sim := cosineSimilarity(queryEmb, emb)
		all = append(all, scored{id: id, score: sim})
	}

	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	if len(all) > limit {
		all = all[:limit]
	}

	results := make([]rankedEntry, len(all))
	for i, s := range all {
		results[i] = rankedEntry{entryID: s.id, score: s.score}
	}
	return results, nil
}

// rrfFuse merges BM25/vector ranked lists with Reciprocal Rank Fusion so hybrid search rewards agreement between retrieval modes.
func rrfFuse(lists ...[]rankedEntry) []rankedEntry {
	scores := make(map[int64]float64)
	for _, list := range lists {
		for rank, entry := range list {
			scores[entry.entryID] += 1.0 / float64(rrfK+rank+1)
		}
	}

	result := make([]rankedEntry, 0, len(scores))
	for id, score := range scores {
		result = append(result, rankedEntry{entryID: id, score: score})
	}
	return result
}

// loadResults hydrates ranked entry IDs, applies hierarchy weighting, and returns ordered MCP search results.
func (s *SearchStore) loadResults(ranked []rankedEntry) ([]SearchResult, error) {
	if len(ranked) == 0 {
		return []SearchResult{}, nil
	}

	scoreMap := make(map[int64]float64, len(ranked))
	ids := make([]interface{}, len(ranked))
	placeholders := make([]string, len(ranked))
	for i, r := range ranked {
		scoreMap[r.entryID] = r.score
		ids[i] = r.entryID
		placeholders[i] = "?"
	}

	rows, err := s.db.Query(
		"SELECT id, source, source_id, content_type, title, content, metadata FROM search_entries WHERE id IN ("+
			strings.Join(placeholders, ",")+
			")", ids...)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()

	resultMap := make(map[int64]SearchResult)
	for rows.Next() {
		var id int64
		var source, sourceID, contentType, title, content string
		var metadata sql.NullString
		if rows.Scan(&id, &source, &sourceID, &contentType, &title, &content, &metadata) != nil { // nocov
			continue
		}
		weight := hierarchyWeights[contentType]
		if weight == 0 {
			weight = 1.0
		}

		var meta json.RawMessage
		if metadata.Valid && metadata.String != "" {
			meta = json.RawMessage(metadata.String)
		}

		resultMap[id] = SearchResult{
			Source:       source,
			ContentType:  contentType,
			Title:        title,
			Content:      content,
			Score:        scoreMap[id] * weight,
			Metadata:     meta,
			MetadataHint: searchMetadataHint(source, contentType),
		}
	}

	results := make([]SearchResult, 0, len(ranked))
	for _, r := range ranked {
		if res, ok := resultMap[r.entryID]; ok {
			results = append(results, res)
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results, nil
}

// searchMetadataHint returns follow-up guidance for a source/content-type pair so MCP callers can pivot correctly.
func searchMetadataHint(source, contentType string) string {
	return searchMetadataHints[source+":"+contentType]
}

// SearchIndexStats holds summary statistics for the search index.
type SearchIndexStats struct {
	Entries  int
	Embedded int
}

// IndexStats returns entry and embedding counts so info/status surfaces can report indexing progress without loading results.
func (s *SearchStore) IndexStats() SearchIndexStats {
	stats := SearchIndexStats{}
	s.db.QueryRow("SELECT COUNT(*) FROM search_entries").Scan(&stats.Entries)
	s.db.QueryRow("SELECT COUNT(*) FROM search_embeddings").Scan(&stats.Embedded)
	return stats
}

// ReadOnlySearchIndexStats opens search.db read-only and returns index stats
// without needing an embedder. Returns zero stats if the DB doesn't exist.
func ReadOnlySearchIndexStats(dir string) SearchIndexStats {
	dbPath := filepath.Join(dir, "search.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return SearchIndexStats{}
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", dbPath))
	if err != nil { // nocov
		return SearchIndexStats{}
	}
	defer db.Close()

	store := &SearchStore{db: db}
	return store.IndexStats()
}

// ---------- Vector math helpers ----------

// cosineSimilarity scores two equal-length vectors so semantic search can rank embedding matches.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	if vek32.Norm(a) == 0 || vek32.Norm(b) == 0 {
		return 0
	}
	return float64(vek32.CosineSimilarity(a, b))
}

// float32sToBytes packs an embedding vector into SQLite blob bytes for storage.
func float32sToBytes(f []float32) []byte {
	buf := make([]byte, len(f)*4)
	for i, v := range f {
		bits := math.Float32bits(v)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// bytesToFloat32s unpacks an embedding blob into float32 values for similarity scoring.
func bytesToFloat32s(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	f := make([]float32, len(b)/4)
	for i := range f {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		f[i] = math.Float32frombits(bits)
	}
	return f
}
