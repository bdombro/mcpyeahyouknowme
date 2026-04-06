package gsuite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

var docsAppDef = &appDef{
	name:          "docs",
	displayName:   "Google Docs",
	initSchema:    initDocsSchema,
	syncFunc:      syncDocs,
	registerTools: registerDocsTools,
	searchEntries: docsSearchEntries,
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "docs_documents") }, // nocov
	tablesToDrop:  []string{"docs_documents", "docs_documents_fts"},
}

// initDocsSchema creates the Docs tables, FTS index, and triggers used by sync and MCP reads.
func initDocsSchema(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS docs_documents (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		modified_time TEXT NOT NULL,
		created_time TEXT NOT NULL,
		web_view_link TEXT,
		owners TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}

	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS docs_documents_fts USING fts5(
		title, content, owners,
		content='docs_documents',
		content_rowid='rowid'
	);

	CREATE TRIGGER IF NOT EXISTS docs_documents_ai AFTER INSERT ON docs_documents BEGIN
		INSERT INTO docs_documents_fts(rowid, title, content, owners)
		VALUES (new.rowid, new.title, new.content, new.owners);
	END;

	CREATE TRIGGER IF NOT EXISTS docs_documents_ad AFTER DELETE ON docs_documents BEGIN
		DELETE FROM docs_documents_fts WHERE rowid = old.rowid;
	END;

	CREATE TRIGGER IF NOT EXISTS docs_documents_au AFTER UPDATE ON docs_documents BEGIN
		INSERT INTO docs_documents_fts(docs_documents_fts, rowid, title, content, owners) VALUES('delete', old.rowid, old.title, old.content, old.owners);
		INSERT INTO docs_documents_fts(rowid, title, content, owners) VALUES (new.rowid, new.title, new.content, new.owners);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	db.Exec("INSERT INTO docs_documents_fts(docs_documents_fts) VALUES('rebuild')")
	return nil
}

// syncDocs refreshes synced Docs metadata/content into SQLite and deletes rows for documents no longer returned by Drive.
func syncDocs(sctx syncContext) error { // nocov
	ctx := sctx.Ctx.(context.Context)
	sctx.SetStatus("syncing")
	defer sctx.SetStatus("idle")

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Drive service: %w", err)
	}
	docsService, err := docs.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Docs service: %w", err)
	}

	remoteIDs := make(map[string]bool)
	var updatedCount int
	pageToken := ""
	for {
		fileList := driveService.Files.List().
			Q("mimeType='application/vnd.google-apps.document'").
			Fields("nextPageToken, files(id, name, modifiedTime, createdTime, webViewLink, owners(displayName, emailAddress))").
			IncludeItemsFromAllDrives(true).
			SupportsAllDrives(true).
			PageSize(100)
		if pageToken != "" { // nocov
			fileList = fileList.PageToken(pageToken)
		}
		res, err := fileList.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list documents: %w", err)
		}
		for _, file := range res.Files {
			remoteIDs[file.Id] = true
			var localMod string
			sctx.DB.QueryRow("SELECT modified_time FROM docs_documents WHERE id = ?", file.Id).Scan(&localMod)
			if localMod == file.ModifiedTime {
				continue
			}
			doc, err := docsService.Documents.Get(file.Id).Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to fetch document %s: %v\n", file.Id, err)
				continue
			}
			record := buildDocsRecord(file, doc, sctx.SelfEmail)
			sctx.DB.Exec(`INSERT OR REPLACE INTO docs_documents
				(id, title, content, modified_time, created_time, web_view_link, owners, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
				record.ID, record.Title, record.Content, record.ModifiedTime,
				record.CreatedTime, record.WebViewLink, record.Owners)
			updatedCount++
			sctx.SetStatus(fmt.Sprintf("syncing:%d", updatedCount))
		}
		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}
	deleteOrphanedRows(sctx.DB, "docs_documents", remoteIDs)
	fmt.Printf("Google Docs sync: %d updated\n", updatedCount)
	return nil
}

type docsRecord struct {
	ID           string
	Title        string
	Content      string
	ModifiedTime string
	CreatedTime  string
	WebViewLink  string
	Owners       string
}

// buildDocsRecord flattens Drive/Docs API payloads into one stored document row for SQLite.
func buildDocsRecord(file *drive.File, doc *docs.Document, selfEmail string) docsRecord {
	if file == nil {
		return docsRecord{}
	}
	return docsRecord{
		ID:           file.Id,
		Title:        file.Name,
		Content:      extractDocumentText(doc),
		ModifiedTime: file.ModifiedTime,
		CreatedTime:  file.CreatedTime,
		WebViewLink:  file.WebViewLink,
		Owners:       formatDriveOwners(file.Owners, selfEmail),
	}
}

// extractDocumentText concatenates paragraph text from a Docs document so search and get-document can return plain text.
func extractDocumentText(doc *docs.Document) string {
	var text string
	for _, element := range doc.Body.Content {
		if element.Paragraph != nil {
			for _, elem := range element.Paragraph.Elements {
				if elem.TextRun != nil {
					text += elem.TextRun.Content
				}
			}
		}
	}
	return text
}

// registerDocsTools wires the local-DB Docs read tools into MCP so clients can search or fetch synced docs without live Google calls.
func registerDocsTools(src *Source, prefix string, s toolAdder) {
	s.AddTool(core.NewReadOnlyTool(prefix+"docs_search",
		core.ToolDescription("Search across all Google Docs", `{"query":"quarterly roadmap","limit":5}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleDocsSearch(ctx, src, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"docs_get_document",
		core.ToolDescription("Get full content of a specific Google Doc by ID", `{"document_id":"1AbcDefGhIj"}`),
		mcp.WithString("document_id", mcp.Required(), mcp.Description("Google Doc ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleDocsGetDocument(ctx, src, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"docs_list_recent",
		core.ToolDescription("List recently modified Google Docs", `{"limit":10}`),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleDocsListRecent(ctx, src, req)
	})
}

// handleDocsSearch runs local FTS for req `query`/`limit`, returning snippet hits from synced docs instead of calling Google live.
func handleDocsSearch(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, errResult := core.RequireStringArgument(req, "query", `{"query":"quarterly roadmap","limit":5}`)
	if errResult != nil {
		return errResult, nil
	}
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT d.id, d.title, snippet(docs_documents_fts, 1, '<mark>', '</mark>', '...', 32) as snippet,
		       d.modified_time, d.created_time, d.web_view_link, d.owners
		FROM docs_documents_fts
		JOIN docs_documents d ON d.rowid = docs_documents_fts.rowid
		WHERE docs_documents_fts MATCH ?
		ORDER BY rank LIMIT ?`, query, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, snippet, modTime, createTime, link, owners string
		if err := rows.Scan(&id, &title, &snippet, &modTime, &createTime, &link, &owners); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "snippet": snippet,
			"modified_time": modTime, "created_time": createTime, "link": link, "owners": owners,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

// handleDocsGetDocument looks up req `document_id` in SQLite and returns the stored body plus metadata, or a tool error if missing.
func handleDocsGetDocument(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	docID, errResult := core.RequireStringArgument(req, "document_id", `{"document_id":"1AbcDefGhIj"}`)
	if errResult != nil {
		return errResult, nil
	}
	if src.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}
	var title, content, modTime, createTime, link, owners string
	err := src.db.QueryRow(`SELECT title, content, modified_time, created_time, web_view_link, owners
		FROM docs_documents WHERE id = ?`, docID).Scan(&title, &content, &modTime, &createTime, &link, &owners)
	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Document not found"), nil
	}
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve document: %v", err)), nil
	}
	return core.JsonResult(map[string]interface{}{
		"id": docID, "title": title, "content": content,
		"modified_time": modTime, "created_time": createTime, "link": link, "owners": owners,
	})
}

// handleDocsListRecent returns the newest synced docs from SQLite for req `limit`, with an empty JSON payload when no DB is available.
func handleDocsListRecent(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 20)
	if src.db == nil {
		return mcp.NewToolResultText("{\"documents\":[],\"count\":0}"), nil
	}
	rows, err := src.db.Query(`SELECT id, title, modified_time, created_time, web_view_link, owners
		FROM docs_documents ORDER BY modified_time DESC LIMIT ?`, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list documents: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, modTime, createTime, link, owners string
		if err := rows.Scan(&id, &title, &modTime, &createTime, &link, &owners); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "modified_time": modTime, "created_time": createTime, "link": link, "owners": owners,
		})
	}
	return core.JsonResult(map[string]interface{}{"documents": results, "count": len(results)})
}

// docsSearchEntries turns synced doc rows into title, owner, and chunked body entries so global search can index them incrementally.
func docsSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT id, title, content, modified_time, owners FROM docs_documents`)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()
	var entries []core.SearchEntry
	for rows.Next() {
		var id, title, content, modTime, owners string
		if err := rows.Scan(&id, &title, &content, &modTime, &owners); err != nil { // nocov
			continue
		}
		entries = append(entries, buildContentEntries(sourceName, id, title, content, modTime, owners,
			"document_title", "document_owner", "document_content", "document_id")...)
	}
	return entries, nil
}

// buildContentEntries creates title + owner + chunked content SearchEntries.
// Shared by docs, sheets, and slides.
func buildContentEntries(sourceName, id, title, content, modTime, owners,
	titleType, ownerType, contentType, idField string) []core.SearchEntry {
	title = strings.ToValidUTF8(title, "")
	content = strings.ToValidUTF8(content, "")
	owners = strings.ToValidUTF8(owners, "")
	var entries []core.SearchEntry
	baseMeta := map[string]interface{}{idField: id, "modified_time": modTime}
	if owners != "" {
		baseMeta["owners"] = owners
	}
	indexedTitle := title
	if owners != "" {
		indexedTitle = owners + " — " + title
	}
	metadata, _ := json.Marshal(baseMeta)
	entries = append(entries, core.SearchEntry{
		Source: sourceName, SourceID: id, ContentType: titleType,
		Title: title, Content: indexedTitle, Metadata: metadata,
	})
	if owners != "" {
		ownerMeta, _ := json.Marshal(baseMeta)
		entries = append(entries, core.SearchEntry{
			Source: sourceName, SourceID: id, ContentType: ownerType,
			Title: title, Content: owners, Metadata: ownerMeta,
		})
	}
	if len(content) > 0 {
		contentWithOwners := content
		if owners != "" {
			contentWithOwners = "Owners: " + owners + "\n\n" + content
		}
		chunks := splitDriveContentChunks(contentWithOwners, core.ChunkMaxChars)
		for i, chunk := range chunks {
			if core.IsLowValueContent(chunk) {
				continue
			}
			chunkMeta, _ := json.Marshal(map[string]interface{}{
				idField: id, "title": title, "chunk_index": i, "modified_time": modTime,
			})
			entries = append(entries, core.SearchEntry{
				Source: sourceName, SourceID: id, ContentType: contentType,
				Title: title, Content: chunk, Metadata: chunkMeta,
			})
		}
	}
	return entries
}

// splitDriveContentChunks breaks Drive-derived content into UTF-8-safe chunks
// within the FTS character budget for Docs, Sheets, and Slides.
func splitDriveContentChunks(text string, limit int) []string {
	if limit <= 0 || text == "" {
		return nil
	}
	text = strings.ToValidUTF8(text, "")
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)+limit-1)/limit)
	for len(runes) > 0 {
		end := limit
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}

// --- shared helpers ---

// formatDriveOwners formats Drive owners, excluding selfEmail, so titles and metadata keep useful collaborator context.
func formatDriveOwners(owners []*drive.User, selfEmail string) string {
	parts := make([]string, 0, len(owners))
	for _, o := range owners {
		if selfEmail != "" && strings.EqualFold(o.EmailAddress, selfEmail) {
			continue
		}
		if o.DisplayName != "" && o.EmailAddress != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", o.DisplayName, o.EmailAddress))
		} else if o.EmailAddress != "" {
			parts = append(parts, o.EmailAddress)
		} else if o.DisplayName != "" {
			parts = append(parts, o.DisplayName)
		}
	}
	return strings.Join(parts, ", ")
}

// deleteOrphanedRows deletes local rows whose IDs disappeared from the latest remote listing.
func deleteOrphanedRows(db *sql.DB, table string, remoteIDs map[string]bool) {
	rows, err := db.Query("SELECT id FROM " + table)
	if err != nil { // nocov
		return
	}
	defer rows.Close()
	var toDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil { // nocov
			continue
		}
		if !remoteIDs[id] {
			toDelete = append(toDelete, id)
		}
	}
	rows.Close()
	for _, id := range toDelete {
		db.Exec("DELETE FROM "+table+" WHERE id = ?", id)
	}
}

// countTable returns the row count for one table so info output can summarize synced app data.
func countTable(db *sql.DB, table string) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
	return n, err
}
