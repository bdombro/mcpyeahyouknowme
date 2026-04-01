package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"mcpyeahyouknowme/core"
)

// SearchEntry is an alias for core.SearchEntry for backward compatibility.
type SearchEntry = core.SearchEntry

// SearchResult is returned by the global search MCP tool.
type SearchResult struct {
	Source      string          `json:"source"`
	ContentType string          `json:"content_type"`
	Title       string          `json:"title"`
	Content     string          `json:"content"`
	Score       float64         `json:"score"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// Hierarchy weights: name matches are most valuable, then participants, then content.
var hierarchyWeights = map[string]float64{
	// WhatsApp
	"chat_name":   3.0,
	"participant": 2.0,
	"message":     1.0,
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
	embedder EmbedderInterface // nil = BM25-only mode
}

// Close releases the search database connection.
func (s *SearchStore) Close() error {
	return s.db.Close()
}

// IndexEntries upserts entries into the search index and computes embeddings
// for new/changed entries when an embedder is available.
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

	if s.embedder != nil {
		return s.computeEmbeddings()
	}
	return nil
}

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

// computeEmbeddings generates embeddings for entries that don't have one yet.
func (s *SearchStore) computeEmbeddings() error {
	rows, err := s.db.Query(`
		SELECT e.id, e.title, e.content
		FROM search_entries e
		LEFT JOIN search_embeddings se ON e.id = se.entry_id
		WHERE se.entry_id IS NULL`)
	if err != nil { // nocov
		return err
	}
	defer rows.Close()

	type pending struct {
		id   int64
		text string
	}
	var batch []pending
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
		// Skip empty texts to avoid tokenizer crashes
		if strings.TrimSpace(text) == "" {
			continue
		}
		batch = append(batch, pending{id: id, text: text})
	}

	if len(batch) == 0 {
		return nil
	}

	texts := make([]string, len(batch))
	for i, p := range batch {
		texts[i] = p.text
	}

	embeddings, err := s.embedder.EmbedTexts(texts, 64)
	if err != nil {
		return fmt.Errorf("embed texts: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil { // nocov
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO search_embeddings (entry_id, embedding) VALUES (?, ?)")
	if err != nil { // nocov
		return err
	}
	defer stmt.Close()

	for i, emb := range embeddings {
		// Skip entries that had empty text (nil embedding)
		if emb == nil || len(emb) == 0 {
			continue
		}
		blob := float32sToBytes(emb)
		if _, err := stmt.Exec(batch[i].id, blob); err != nil { // nocov
			return err
		}
	}
	return tx.Commit()
}

// UpdateSourceTimestamp records when a source was last indexed.
func (s *SearchStore) UpdateSourceTimestamp(source string, t time.Time) {
	s.db.Exec(`INSERT INTO search_meta (source, last_indexed) VALUES (?, ?)
		ON CONFLICT(source) DO UPDATE SET last_indexed=excluded.last_indexed`,
		source, t.Format(time.RFC3339))
}

// LastIndexed returns the time a source was last indexed, or zero time if never.
func (s *SearchStore) LastIndexed(source string) time.Time {
	var ts sql.NullString
	s.db.QueryRow("SELECT last_indexed FROM search_meta WHERE source = ?", source).Scan(&ts)
	if ts.Valid {
		t, _ := time.Parse(time.RFC3339, ts.String)
		return t
	}
	return time.Time{}
}

// Search performs hybrid BM25 + vector search with RRF fusion and hierarchy weighting.
func (s *SearchStore) Search(query string, limit int, sourceFilter, typeFilter string) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	bm25Results := s.bm25SearchEntries(query, limit*5, sourceFilter, typeFilter)

	var vectorResults []rankedEntry
	if s.embedder != nil {
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

func (s *SearchStore) bm25SearchEntries(query string, limit int, sourceFilter, typeFilter string) []rankedEntry {
	safeQuery := strings.ReplaceAll(query, `"`, `""`)
	ftsQuery := `"` + safeQuery + `"`

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

// rrfFuse combines ranked lists using Reciprocal Rank Fusion.
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

// Hierarchy weighting is applied in loadResults where we have access to
// the content_type for each entry.

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
			Source:      source,
			ContentType: contentType,
			Title:       title,
			Content:     content,
			Score:       scoreMap[id] * weight,
			Metadata:    meta,
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

// ---------- Vector math helpers ----------

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

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
