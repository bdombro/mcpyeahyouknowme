package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"mcpyeahyouknowme/core"
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

// indexKey identifies one indexed row within a source so prune passes can
// retain current entries without holding full SearchEntry payloads in memory.
type indexKey struct {
	SourceID    string
	ContentType string
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

// SearchStore manages the combined search index across all data sources.
type SearchStore struct {
	db      *sql.DB
	bulkFTS bool
}

var searchStoreRebuildFTS = func(db *sql.DB) error {
	if _, err := db.Exec(`INSERT INTO search_fts(search_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("rebuild search fts: %w", err) // nocov
	}
	return nil
}

// Close releases the search DB handle so daemon, MCP, or CLI callers do not leave SQLite connections open.
func (s *SearchStore) Close() error {
	return s.db.Close()
}

// Suspends row-by-row FTS trigger maintenance so full rebuilds can upsert and
// prune many rows before paying one final rebuild cost.
func (s *SearchStore) BeginBulkIndex() error {
	if s.bulkFTS {
		return nil
	}
	if err := dropSearchFTSTriggers(s.db); err != nil {
		return err
	}
	s.bulkFTS = true
	return nil
}

// Restores FTS triggers and rebuilds the FTS table from live search_entries
// contents so query-time BM25 matches the latest bulk-loaded rows.
func (s *SearchStore) EndBulkIndex() error {
	if !s.bulkFTS {
		return nil
	}
	if err := createSearchFTSTriggers(s.db); err != nil {
		return err
	}
	s.bulkFTS = false
	return searchStoreRebuildFTS(s.db)
}

// Clears all search entries, FTS rows, and source timestamps so a full rebuild starts from an empty index.
func (s *SearchStore) Clear() error {
	_, err := s.db.Exec(`
		DELETE FROM search_entries;
		INSERT INTO search_fts(search_fts) VALUES('rebuild');
		DELETE FROM search_meta;
	`)
	if err != nil {
		return fmt.Errorf("clear search index: %w", err)
	}
	return nil
}

// Deletes all indexed rows for one source so resets stop surfacing stale content in search.
func (s *SearchStore) DeleteBySource(source string) error {
	if _, err := s.db.Exec(`DELETE FROM search_entries WHERE source = ?`, source); err != nil {
		return fmt.Errorf("delete search entries for source %s: %w", source, err)
	}
	return nil
}

// IndexEntries upserts entries into the search index so all sources become
// searchable before any background work begins.
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
	if !s.bulkFTS {
		s.rebuildFTSIfNeeded()
	}
	return nil
}

// PruneSourceKeys removes stale rows for one source after a full pass so the
// stored index exactly matches the current emitted source_id/content_type keys.
func (s *SearchStore) PruneSourceKeys(source string, current []indexKey) error {
	tx, err := s.db.Begin()
	if err != nil { // nocov
		return fmt.Errorf("begin prune for %s: %w", source, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TEMP TABLE IF NOT EXISTS prune_keep_keys (
			source_id TEXT NOT NULL,
			content_type TEXT NOT NULL,
			PRIMARY KEY (source_id, content_type)
		)`); err != nil { // nocov
		return fmt.Errorf("create prune keys for %s: %w", source, err)
	}
	if _, err := tx.Exec(`DELETE FROM prune_keep_keys`); err != nil { // nocov
		return fmt.Errorf("clear prune keys for %s: %w", source, err)
	}

	if len(current) > 0 {
		stmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO prune_keep_keys (source_id, content_type)
			VALUES (?, ?)`)
		if err != nil { // nocov
			return fmt.Errorf("prepare prune keys for %s: %w", source, err)
		}
		defer stmt.Close()

		for _, key := range current {
			if _, err := stmt.Exec(key.SourceID, key.ContentType); err != nil { // nocov
				return fmt.Errorf("insert prune key for %s/%s/%s: %w", source, key.SourceID, key.ContentType, err)
			}
		}
	}

	if _, err := tx.Exec(`
		DELETE FROM search_entries
		WHERE source = ?
		  AND NOT EXISTS (
			SELECT 1
			FROM prune_keep_keys
			WHERE prune_keep_keys.source_id = search_entries.source_id
			  AND prune_keep_keys.content_type = search_entries.content_type
		)`, source); err != nil { // nocov
		return fmt.Errorf("prune stale entries for %s: %w", source, err)
	}

	if _, err := tx.Exec(`DELETE FROM prune_keep_keys`); err != nil { // nocov
		return fmt.Errorf("reset prune keys for %s: %w", source, err)
	}
	if err := tx.Commit(); err != nil { // nocov
		return fmt.Errorf("commit prune for %s: %w", source, err)
	}
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
		_ = searchStoreRebuildFTS(s.db)
	}
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

// Search runs BM25 keyword retrieval for query and returns weighted top results.
func (s *SearchStore) Search(query string, limit int, sourceFilter, typeFilter string) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	ranked := s.bm25SearchEntries(query, limit*5, sourceFilter, typeFilter)

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	return s.loadResults(ranked)
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

// sanitizeFTSQuery tokenizes the query into OR-joined prefix terms so BM25 search
// returns any document containing a word that starts with any query keyword.
// OR semantics maximise recall; BM25 naturally ranks documents matching more terms higher.
// Tokens shorter than 3 characters are dropped before building the OR expression to avoid
// overly broad prefix matches (e.g. "me*" matching "meeting", "message", "member", etc.).
// If all tokens are short, the filter is skipped so single-letter queries still work.
func sanitizeFTSQuery(query string) string {
	tokens := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(tokens) == 0 {
		safeQuery := strings.ReplaceAll(strings.TrimSpace(query), `"`, `""`)
		return `"` + safeQuery + `"`
	}

	significant := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if len(tok) >= 3 {
			significant = append(significant, tok)
		}
	}
	if len(significant) == 0 {
		significant = tokens
	}

	parts := make([]string, 0, len(significant))
	for _, token := range significant {
		safeToken := strings.ReplaceAll(token, `"`, `""`)
		parts = append(parts, `"`+safeToken+`"*`)
	}
	return strings.Join(parts, " OR ")
}

// loadResults hydrates ranked entry IDs, applies hierarchy weighting, and returns ordered search results.
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
			Score:        -scoreMap[id] * weight,
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
	Entries int
}

// IndexStats returns entry count so info/status surfaces can report indexing progress without loading results.
func (s *SearchStore) IndexStats() SearchIndexStats {
	stats := SearchIndexStats{}
	s.db.QueryRow("SELECT COUNT(*) FROM search_entries").Scan(&stats.Entries)
	return stats
}

// ReadOnlySearchIndexStats opens search.db read-only and returns index stats
// without needing a full store. Returns zero stats if the DB doesn't exist.
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
