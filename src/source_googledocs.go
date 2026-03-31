package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// GoogleDocsSource implements DataSource for Google Docs.
type GoogleDocsSource struct {
	db    *sql.DB
	token *oauth2.Token
}

// NewGoogleDocsSource creates a new Google Docs data source.
func NewGoogleDocsSource() (*GoogleDocsSource, error) {
	dbPath := filepath.Join(dataDir(), "googledocs.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_busy_timeout=30000", dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open googledocs database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")

	// Create tables
	if err := initGoogleDocsDB(db); err != nil {
		db.Close()
		return nil, err
	}

	src := &GoogleDocsSource{db: db}

	// Load saved token if exists
	if err := src.loadToken(); err == nil {
		// Token loaded successfully
	}

	return src, nil
}

func (g *GoogleDocsSource) Name() string        { return "googledocs" }
func (g *GoogleDocsSource) Description() string { return "Google Docs" }
func (g *GoogleDocsSource) Close() error        { return g.db.Close() }

func (g *GoogleDocsSource) RequiresAuth() bool {
	return true
}

// initGoogleDocsDB creates the database schema.
func initGoogleDocsDB(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		modified_time TEXT NOT NULL,
		created_time TEXT NOT NULL,
		web_view_link TEXT,
		last_synced TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
		title, content,
		content='documents',
		content_rowid='rowid'
	);

	-- Triggers to keep FTS index in sync
	CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
		INSERT INTO documents_fts(rowid, title, content)
		VALUES (new.rowid, new.title, new.content);
	END;

	CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
		DELETE FROM documents_fts WHERE rowid = old.rowid;
	END;

	CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
		INSERT INTO documents_fts(documents_fts, rowid, title, content) VALUES('delete', old.rowid, old.title, old.content);
		INSERT INTO documents_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
	END;
	`

	_, err := db.Exec(schema)
	return err
}

// loadToken loads the OAuth token from disk.
func (g *GoogleDocsSource) loadToken() error {
	tokenPath := filepath.Join(dataDir(), "googledocs_token.json")
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}

	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return err
	}

	g.token = &token
	return nil
}

// saveToken saves the OAuth token to disk.
func (g *GoogleDocsSource) saveToken(token *oauth2.Token) error {
	g.token = token
	tokenPath := filepath.Join(dataDir(), "googledocs_token.json")
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return os.WriteFile(tokenPath, data, 0600)
}

// isAuthenticated checks if we have a valid token.
func (g *GoogleDocsSource) isAuthenticated() bool {
	if g.token == nil {
		return false
	}
	// OAuth2 library handles access token refresh automatically if we have a refresh token
	// Only return false if we don't have a refresh token (which means reauth is needed)
	if g.token.RefreshToken == "" && g.token.Expiry.Before(time.Now()) {
		return false
	}
	return true
}

// getOAuthConfig returns the OAuth2 configuration using credentials baked
// into the binary at build time via -ldflags.
func (g *GoogleDocsSource) getOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     GoogleClientID,
		ClientSecret: GoogleClientSecret,
		RedirectURL:  "http://127.0.0.1:8085",
		Scopes: []string{
			docs.DocumentsReadonlyScope,
			drive.DriveReadonlyScope,
		},
		Endpoint: google.Endpoint,
	}
}

// RegisterTools adds Google Docs MCP tools to the server.
func (g *GoogleDocsSource) RegisterTools(s *server.MCPServer) {
	prefix := g.Name() + "_"
	s.AddTool(mcp.NewTool(prefix+"search",
		mcp.WithDescription("Search across all Google Docs"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default 10)")),
	), g.handleSearch)

	s.AddTool(mcp.NewTool(prefix+"get_document",
		mcp.WithDescription("Get full content of a specific Google Doc by ID"),
		mcp.WithString("document_id",
			mcp.Required(),
			mcp.Description("Google Doc ID")),
	), g.handleGetDocument)

	s.AddTool(mcp.NewTool(prefix+"list_recent",
		mcp.WithDescription("List recently modified Google Docs"),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default 20)")),
	), g.handleListRecent)
}

// handleSearch searches Google Docs.
func (g *GoogleDocsSource) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	
	args := req.GetArguments()
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	rows, err := g.db.Query(`
		SELECT d.id, d.title, snippet(documents_fts, 1, '<mark>', '</mark>', '...', 32) as snippet, 
		       d.modified_time, d.web_view_link
		FROM documents_fts
		JOIN documents d ON d.rowid = documents_fts.rowid
		WHERE documents_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, title, snippet, modifiedTime, webViewLink string
		if err := rows.Scan(&id, &title, &snippet, &modifiedTime, &webViewLink); err != nil {
			continue
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"title":         title,
			"snippet":       snippet,
			"modified_time": modifiedTime,
			"link":          webViewLink,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"query":   query,
		"results": results,
		"count":   len(results),
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}

// handleGetDocument retrieves a full document.
func (g *GoogleDocsSource) handleGetDocument(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	docID, _ := req.RequireString("document_id")

	var title, content, modifiedTime, webViewLink string
	err := g.db.QueryRow(`
		SELECT title, content, modified_time, web_view_link
		FROM documents
		WHERE id = ?
	`, docID).Scan(&title, &content, &modifiedTime, &webViewLink)

	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Document not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve document: %v", err)), nil
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"id":            docID,
		"title":         title,
		"content":       content,
		"modified_time": modifiedTime,
		"link":          webViewLink,
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}

// handleListRecent lists recently modified documents.
func (g *GoogleDocsSource) handleListRecent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	rows, err := g.db.Query(`
		SELECT id, title, modified_time, web_view_link
		FROM documents
		ORDER BY modified_time DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list documents: %v", err)), nil
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, title, modifiedTime, webViewLink string
		if err := rows.Scan(&id, &title, &modifiedTime, &webViewLink); err != nil {
			continue
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"title":         title,
			"modified_time": modifiedTime,
			"link":          webViewLink,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"documents": results,
		"count":     len(results),
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}

// SearchEntries returns all documents for the global search index.
func (g *GoogleDocsSource) SearchEntries() ([]SearchEntry, error) {
	rows, err := g.db.Query(`
		SELECT id, title, content, modified_time
		FROM documents
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []SearchEntry
	for rows.Next() {
		var id, title, content, modifiedTime string
		if err := rows.Scan(&id, &title, &content, &modifiedTime); err != nil {
			continue
		}

		// Add document title
		metadata, _ := json.Marshal(map[string]interface{}{
			"document_id":   id,
			"modified_time": modifiedTime,
		})
		entries = append(entries, SearchEntry{
			Source:      g.Name(),
			SourceID:    id,
			ContentType: "document_title",
			Title:       title,
			Content:     title,
			Metadata:    metadata,
		})

		// Add document content (chunked if too long)
		if len(content) > 0 {
			chunkSize := 5000
			for i := 0; i < len(content); i += chunkSize {
				end := i + chunkSize
				if end > len(content) {
					end = len(content)
				}
				chunk := content[i:end]

				chunkMeta, _ := json.Marshal(map[string]interface{}{
					"document_id":   id,
					"document_title": title,
					"chunk_index":   i / chunkSize,
					"modified_time": modifiedTime,
				})
				entries = append(entries, SearchEntry{
					Source:      g.Name(),
					SourceID:    id,
					ContentType: "document_content",
					Title:       title,
					Content:     chunk,
					Metadata:    chunkMeta,
				})
			}
		}
	}

	return entries, nil
}

// StartCore runs the periodic sync daemon.
func (g *GoogleDocsSource) StartCore(ctx context.Context) error {
	fmt.Println("Starting Google Docs sync daemon...")

	if !g.isAuthenticated() {
		return fmt.Errorf("not authenticated - run 'mcpyeahyouknowme googledocs login' first")
	}

	// Initial sync
	if err := g.syncDocuments(ctx); err != nil {
		fmt.Printf("Warning: Initial sync failed: %v\n", err)
	}

	// Sync every 15 minutes
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Google Docs sync daemon stopped")
			return nil
		case <-ticker.C:
			fmt.Println("Running scheduled Google Docs sync...")
			if err := g.syncDocuments(ctx); err != nil {
				fmt.Printf("Sync error: %v\n", err)
			}
		}
	}
}

// syncDocuments fetches and updates documents from Google Docs API.
func (g *GoogleDocsSource) syncDocuments(ctx context.Context) error {
	config := g.getOAuthConfig()
	client := config.Client(ctx, g.token)

	// Create Drive service to list documents
	driveService, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create Drive service: %w", err)
	}

	// Create Docs service to fetch content
	docsService, err := docs.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create Docs service: %w", err)
	}

	// Get last sync time
	lastSync := g.getLastSyncTime()

	// List all Google Docs, optionally filtering by modified time
	query := "mimeType='application/vnd.google-apps.document'"
	if !lastSync.IsZero() {
		query += fmt.Sprintf(" and modifiedTime > '%s'", lastSync.Format(time.RFC3339))
	}

	var updatedCount int
	pageToken := ""
	for {
		fileList := driveService.Files.List().
			Q(query).
			Fields("nextPageToken, files(id, name, modifiedTime, createdTime, webViewLink)").
			PageSize(100)

		if pageToken != "" {
			fileList = fileList.PageToken(pageToken)
		}

		res, err := fileList.Do()
		if err != nil {
			return fmt.Errorf("failed to list documents: %w", err)
		}

		for _, file := range res.Files {
			// Fetch document content
			doc, err := docsService.Documents.Get(file.Id).Do()
			if err != nil {
				fmt.Printf("Warning: Failed to fetch document %s: %v\n", file.Id, err)
				continue
			}

			// Extract text content
			content := extractDocumentText(doc)

			// Store in database
			_, err = g.db.Exec(`
				INSERT OR REPLACE INTO documents 
				(id, title, content, modified_time, created_time, web_view_link, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, file.Id, file.Name, content, file.ModifiedTime, file.CreatedTime, 
			   file.WebViewLink, time.Now().Format(time.RFC3339))

			if err != nil {
				fmt.Printf("Warning: Failed to store document %s: %v\n", file.Id, err)
				continue
			}

			updatedCount++
		}

		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}

	// Update last sync time
	g.setLastSyncTime(time.Now())

	fmt.Printf("Sync complete: %d documents updated\n", updatedCount)
	return nil
}

// extractDocumentText extracts plain text from a Google Doc.
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

// getLastSyncTime retrieves the last sync timestamp.
func (g *GoogleDocsSource) getLastSyncTime() time.Time {
	var value string
	err := g.db.QueryRow("SELECT value FROM sync_state WHERE key = 'last_sync'").Scan(&value)
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, value)
	return t
}

// setLastSyncTime updates the last sync timestamp.
func (g *GoogleDocsSource) setLastSyncTime(t time.Time) {
	g.db.Exec(`
		INSERT OR REPLACE INTO sync_state (key, value)
		VALUES ('last_sync', ?)
	`, t.Format(time.RFC3339))
}
