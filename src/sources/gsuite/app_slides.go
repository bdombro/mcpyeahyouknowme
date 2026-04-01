package gsuite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/slides/v1"
)

var slidesAppDef = &appDef{
	name:          "slides",
	displayName:   "Google Slides",
	initSchema:    initSlidesSchema,
	syncFunc:      syncSlides,
	registerTools: registerSlidesTools,
	searchEntries: slidesSearchEntries,
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "slides_presentations") },
	tablesToDrop:  []string{"slides_presentations", "slides_presentations_fts"},
}

func initSlidesSchema(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS slides_presentations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		modified_time TEXT NOT NULL,
		created_time TEXT NOT NULL,
		web_view_link TEXT,
		owners TEXT NOT NULL DEFAULT '',
		slide_count INTEGER NOT NULL DEFAULT 0,
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS slides_presentations_fts USING fts5(
		title, content, owners,
		content='slides_presentations',
		content_rowid='rowid'
	);
	CREATE TRIGGER IF NOT EXISTS slides_presentations_ai AFTER INSERT ON slides_presentations BEGIN
		INSERT INTO slides_presentations_fts(rowid, title, content, owners)
		VALUES (new.rowid, new.title, new.content, new.owners);
	END;
	CREATE TRIGGER IF NOT EXISTS slides_presentations_ad AFTER DELETE ON slides_presentations BEGIN
		DELETE FROM slides_presentations_fts WHERE rowid = old.rowid;
	END;
	CREATE TRIGGER IF NOT EXISTS slides_presentations_au AFTER UPDATE ON slides_presentations BEGIN
		INSERT INTO slides_presentations_fts(slides_presentations_fts, rowid, title, content, owners)
		VALUES('delete', old.rowid, old.title, old.content, old.owners);
		INSERT INTO slides_presentations_fts(rowid, title, content, owners)
		VALUES (new.rowid, new.title, new.content, new.owners);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	db.Exec("INSERT INTO slides_presentations_fts(slides_presentations_fts) VALUES('rebuild')")
	return nil
}

func syncSlides(sctx syncContext) error { // nocov
	ctx := sctx.Ctx.(context.Context)
	sctx.SetStatus("syncing")
	defer sctx.SetStatus("idle")

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Drive service: %w", err)
	}
	slidesService, err := slides.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Slides service: %w", err)
	}

	remoteIDs := make(map[string]bool)
	var updatedCount int
	pageToken := ""
	for {
		fileList := driveService.Files.List().
			Q("mimeType='application/vnd.google-apps.presentation'").
			Fields("nextPageToken, files(id, name, modifiedTime, createdTime, webViewLink, owners(displayName, emailAddress))").
			IncludeItemsFromAllDrives(true).SupportsAllDrives(true).PageSize(100)
		if pageToken != "" { // nocov
			fileList = fileList.PageToken(pageToken)
		}
		res, err := fileList.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list presentations: %w", err)
		}
		for _, file := range res.Files {
			remoteIDs[file.Id] = true
			var localMod string
			sctx.DB.QueryRow("SELECT modified_time FROM slides_presentations WHERE id = ?", file.Id).Scan(&localMod)
			if localMod == file.ModifiedTime {
				continue
			}
			pres, err := slidesService.Presentations.Get(file.Id).Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to fetch presentation %s: %v\n", file.Id, err)
				continue
			}
			content := extractPresentationText(pres)
			owners := formatDriveOwners(file.Owners, sctx.SelfEmail)
			sctx.DB.Exec(`INSERT OR REPLACE INTO slides_presentations
				(id, title, content, modified_time, created_time, web_view_link, owners, slide_count, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
				file.Id, file.Name, content, file.ModifiedTime, file.CreatedTime,
				file.WebViewLink, owners, len(pres.Slides))
			updatedCount++
			sctx.SetStatus(fmt.Sprintf("syncing:%d", updatedCount))
		}
		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}
	deleteOrphanedRows(sctx.DB, "slides_presentations", remoteIDs)
	fmt.Printf("Google Slides sync: %d updated\n", updatedCount)
	return nil
}

func extractPresentationText(pres *slides.Presentation) string {
	var b strings.Builder
	for i, slide := range pres.Slides {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("## Slide %d\n", i+1))
		for _, element := range slide.PageElements {
			if element.Shape != nil && element.Shape.Text != nil {
				for _, te := range element.Shape.Text.TextElements {
					if te.TextRun != nil {
						b.WriteString(te.TextRun.Content)
					}
				}
			}
		}
	}
	return b.String()
}

func registerSlidesTools(src *Source, prefix string, s toolAdder) {
	s.AddTool(mcp.NewTool(prefix+"slides_search",
		mcp.WithDescription("Search across all Google Slides presentations"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleSlidesSearch(src, ctx, req)
	})
	s.AddTool(mcp.NewTool(prefix+"slides_get_presentation",
		mcp.WithDescription("Get full content of a specific Google Slides presentation by ID"),
		mcp.WithString("presentation_id", mcp.Required(), mcp.Description("Presentation ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleSlidesGetPresentation(src, ctx, req)
	})
	s.AddTool(mcp.NewTool(prefix+"slides_list_recent",
		mcp.WithDescription("List recently modified Google Slides presentations"),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleSlidesListRecent(src, ctx, req)
	})
}

func handleSlidesSearch(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT p.id, p.title, snippet(slides_presentations_fts, 1, '<mark>', '</mark>', '...', 32),
		       p.modified_time, p.created_time, p.web_view_link, p.owners, p.slide_count
		FROM slides_presentations_fts
		JOIN slides_presentations p ON p.rowid = slides_presentations_fts.rowid
		WHERE slides_presentations_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, snippet, modTime, createTime, link, owners string
		var slideCount int
		if err := rows.Scan(&id, &title, &snippet, &modTime, &createTime, &link, &owners, &slideCount); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "snippet": snippet, "modified_time": modTime,
			"created_time": createTime, "link": link, "owners": owners, "slide_count": slideCount,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

func handleSlidesGetPresentation(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	presID, _ := req.RequireString("presentation_id")
	if src.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}
	var title, content, modTime, createTime, link, owners string
	var slideCount int
	err := src.db.QueryRow(`SELECT title, content, modified_time, created_time, web_view_link, owners, slide_count
		FROM slides_presentations WHERE id = ?`, presID).Scan(&title, &content, &modTime, &createTime, &link, &owners, &slideCount)
	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Presentation not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve presentation: %v", err)), nil
	}
	return core.JsonResult(map[string]interface{}{
		"id": presID, "title": title, "content": content, "modified_time": modTime,
		"created_time": createTime, "link": link, "owners": owners, "slide_count": slideCount,
	})
}

func handleSlidesListRecent(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 20)
	if src.db == nil {
		return mcp.NewToolResultText("{\"presentations\":[],\"count\":0}"), nil
	}
	rows, err := src.db.Query(`SELECT id, title, modified_time, created_time, web_view_link, owners, slide_count
		FROM slides_presentations ORDER BY modified_time DESC LIMIT ?`, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list presentations: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, modTime, createTime, link, owners string
		var slideCount int
		if err := rows.Scan(&id, &title, &modTime, &createTime, &link, &owners, &slideCount); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "modified_time": modTime, "created_time": createTime,
			"link": link, "owners": owners, "slide_count": slideCount,
		})
	}
	return core.JsonResult(map[string]interface{}{"presentations": results, "count": len(results)})
}

func slidesSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT id, title, content, modified_time, owners FROM slides_presentations`)
	if err != nil {
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
			"presentation_title", "presentation_owner", "presentation_content", "presentation_id")...)
	}
	return entries, nil
}
