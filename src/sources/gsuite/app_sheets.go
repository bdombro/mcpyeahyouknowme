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
	"google.golang.org/api/sheets/v4"
)

var sheetsAppDef = &appDef{
	name:          "sheets",
	displayName:   "Google Sheets",
	initSchema:    initSheetsSchema,
	syncFunc:      syncSheets,
	registerTools: registerSheetsTools,
	searchEntries: sheetsSearchEntries,
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "sheets_spreadsheets") }, // nocov
	tablesToDrop:  []string{"sheets_spreadsheets", "sheets_spreadsheets_fts"},
}

// initSheetsSchema creates the Sheets tables, FTS index, and triggers used by sync and MCP reads.
func initSheetsSchema(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS sheets_spreadsheets (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		modified_time TEXT NOT NULL,
		created_time TEXT NOT NULL,
		web_view_link TEXT,
		owners TEXT NOT NULL DEFAULT '',
		sheet_count INTEGER NOT NULL DEFAULT 0,
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS sheets_spreadsheets_fts USING fts5(
		title, content, owners,
		content='sheets_spreadsheets',
		content_rowid='rowid'
	);
	CREATE TRIGGER IF NOT EXISTS sheets_spreadsheets_ai AFTER INSERT ON sheets_spreadsheets BEGIN
		INSERT INTO sheets_spreadsheets_fts(rowid, title, content, owners)
		VALUES (new.rowid, new.title, new.content, new.owners);
	END;
	CREATE TRIGGER IF NOT EXISTS sheets_spreadsheets_ad AFTER DELETE ON sheets_spreadsheets BEGIN
		DELETE FROM sheets_spreadsheets_fts WHERE rowid = old.rowid;
	END;
	CREATE TRIGGER IF NOT EXISTS sheets_spreadsheets_au AFTER UPDATE ON sheets_spreadsheets BEGIN
		INSERT INTO sheets_spreadsheets_fts(sheets_spreadsheets_fts, rowid, title, content, owners) VALUES('delete', old.rowid, old.title, old.content, old.owners);
		INSERT INTO sheets_spreadsheets_fts(rowid, title, content, owners) VALUES (new.rowid, new.title, new.content, new.owners);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	db.Exec("INSERT INTO sheets_spreadsheets_fts(sheets_spreadsheets_fts) VALUES('rebuild')")
	return nil
}

// syncSheets refreshes synced spreadsheets into SQLite and removes local rows missing from the latest Drive listing.
func syncSheets(sctx syncContext) error { // nocov
	ctx := sctx.Ctx.(context.Context)
	sctx.SetStatus("syncing")
	defer sctx.SetStatus("idle")

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Drive service: %w", err)
	}
	sheetsService, err := sheets.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Sheets service: %w", err)
	}

	remoteIDs := make(map[string]bool)
	var updatedCount int
	pageToken := ""
	for {
		fileList := driveService.Files.List().
			Q("mimeType='application/vnd.google-apps.spreadsheet'").
			Fields("nextPageToken, files(id, name, modifiedTime, createdTime, webViewLink, owners(displayName, emailAddress))").
			IncludeItemsFromAllDrives(true).SupportsAllDrives(true).PageSize(100)
		if pageToken != "" { // nocov
			fileList = fileList.PageToken(pageToken)
		}
		res, err := fileList.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list spreadsheets: %w", err)
		}
		for _, file := range res.Files {
			remoteIDs[file.Id] = true
			var localMod string
			sctx.DB.QueryRow("SELECT modified_time FROM sheets_spreadsheets WHERE id = ?", file.Id).Scan(&localMod)
			if localMod == file.ModifiedTime {
				continue
			}
			ss, err := sheetsService.Spreadsheets.Get(file.Id).Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to fetch spreadsheet %s: %v\n", file.Id, err)
				continue
			}
			record := buildSheetsRecord(file, ss, sctx.SelfEmail)
			sctx.DB.Exec(`INSERT OR REPLACE INTO sheets_spreadsheets
				(id, title, content, modified_time, created_time, web_view_link, owners, sheet_count, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
				record.ID, record.Title, record.Content, record.ModifiedTime,
				record.CreatedTime, record.WebViewLink, record.Owners, record.SheetCount)
			updatedCount++
			sctx.SetStatus(fmt.Sprintf("syncing:%d", updatedCount))
		}
		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}
	deleteOrphanedRows(sctx.DB, "sheets_spreadsheets", remoteIDs)
	fmt.Printf("Google Sheets sync: %d updated\n", updatedCount)
	return nil
}

type sheetsRecord struct {
	ID           string
	Title        string
	Content      string
	ModifiedTime string
	CreatedTime  string
	WebViewLink  string
	Owners       string
	SheetCount   int
}

// buildSheetsRecord flattens Drive and Sheets API payloads into one stored spreadsheet row.
func buildSheetsRecord(file *drive.File, ss *sheets.Spreadsheet, selfEmail string) sheetsRecord {
	if file == nil {
		return sheetsRecord{}
	}
	record := sheetsRecord{
		ID:           file.Id,
		Title:        file.Name,
		ModifiedTime: file.ModifiedTime,
		CreatedTime:  file.CreatedTime,
		WebViewLink:  file.WebViewLink,
		Owners:       formatDriveOwners(file.Owners, selfEmail),
	}
	if ss != nil {
		record.Content = extractSpreadsheetText(ss)
		record.SheetCount = len(ss.Sheets)
	}
	return record
}

// extractSpreadsheetText renders sheet titles and formatted cell values into plain text for storage and search.
func extractSpreadsheetText(ss *sheets.Spreadsheet) string {
	var b strings.Builder
	for i, sheet := range ss.Sheets {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## ")
		b.WriteString(sheet.Properties.Title)
		b.WriteString("\n")
		if sheet.Data == nil {
			continue
		}
		for _, gridData := range sheet.Data {
			for _, row := range gridData.RowData {
				var cells []string
				for _, cell := range row.Values {
					cells = append(cells, cell.FormattedValue)
				}
				b.WriteString(strings.Join(cells, "\t"))
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// registerSheetsTools wires the local-DB Sheets read tools into MCP so clients can query synced spreadsheets without live API calls.
func registerSheetsTools(src *Source, prefix string, s core.ToolAdder) {
	s.AddTool(core.NewReadOnlyTool(prefix+"sheets_search",
		core.ToolDescription("Search across all Google Sheets", `{"query":"headcount","limit":5}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("2–4 keywords extracted from the question; drop filler words; include synonyms for better recall (e.g. 'headcount budget employees')")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleSheetsSearch(ctx, src, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"sheets_get_spreadsheet",
		core.ToolDescription("Get full content of a specific Google Sheet by ID", `{"spreadsheet_id":"1AbcDefGhIj"}`),
		mcp.WithString("spreadsheet_id", mcp.Required(), mcp.Description("Google Spreadsheet ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleSheetsGetSpreadsheet(ctx, src, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"sheets_list_recent",
		core.ToolDescription("List recently modified Google Sheets", `{"limit":10}`),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleSheetsListRecent(ctx, src, req)
	})
}

// handleSheetsSearch runs local FTS for req `query`/`limit`, returning snippet hits from synced spreadsheets rather than calling Google live.
func handleSheetsSearch(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, errResult := core.RequireStringArgument(req, "query", `{"query":"headcount","limit":5}`)
	if errResult != nil {
		return errResult, nil
	}
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT s.id, s.title, snippet(sheets_spreadsheets_fts, 1, '<mark>', '</mark>', '...', 32),
		       s.modified_time, s.created_time, s.web_view_link, s.owners, s.sheet_count
		FROM sheets_spreadsheets_fts
		JOIN sheets_spreadsheets s ON s.rowid = sheets_spreadsheets_fts.rowid
		WHERE sheets_spreadsheets_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, snippet, modTime, createTime, link, owners string
		var sheetCount int
		if err := rows.Scan(&id, &title, &snippet, &modTime, &createTime, &link, &owners, &sheetCount); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "snippet": snippet, "modified_time": modTime,
			"created_time": createTime, "link": link, "owners": owners, "sheet_count": sheetCount,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

// handleSheetsGetSpreadsheet looks up req `spreadsheet_id` in SQLite and returns the stored rendered sheet content plus metadata.
func handleSheetsGetSpreadsheet(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sheetID, errResult := core.RequireStringArgument(req, "spreadsheet_id", `{"spreadsheet_id":"1AbcDefGhIj"}`)
	if errResult != nil {
		return errResult, nil
	}
	if src.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}
	var title, content, modTime, createTime, link, owners string
	var sheetCount int
	err := src.db.QueryRow(`SELECT title, content, modified_time, created_time, web_view_link, owners, sheet_count
		FROM sheets_spreadsheets WHERE id = ?`, sheetID).Scan(&title, &content, &modTime, &createTime, &link, &owners, &sheetCount)
	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Spreadsheet not found"), nil
	}
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve spreadsheet: %v", err)), nil
	}
	return core.JsonResult(map[string]interface{}{
		"id": sheetID, "title": title, "content": content, "modified_time": modTime,
		"created_time": createTime, "link": link, "owners": owners, "sheet_count": sheetCount,
	})
}

// handleSheetsListRecent returns the newest synced spreadsheets from SQLite for req `limit`, or an empty JSON payload when no DB exists.
func handleSheetsListRecent(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 20)
	if src.db == nil {
		return mcp.NewToolResultText("{\"spreadsheets\":[],\"count\":0}"), nil
	}
	rows, err := src.db.Query(`SELECT id, title, modified_time, created_time, web_view_link, owners, sheet_count
		FROM sheets_spreadsheets ORDER BY modified_time DESC LIMIT ?`, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list spreadsheets: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, modTime, createTime, link, owners string
		var sheetCount int
		if err := rows.Scan(&id, &title, &modTime, &createTime, &link, &owners, &sheetCount); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "modified_time": modTime, "created_time": createTime,
			"link": link, "owners": owners, "sheet_count": sheetCount,
		})
	}
	return core.JsonResult(map[string]interface{}{"spreadsheets": results, "count": len(results)})
}

// sheetsSearchEntries turns synced spreadsheet rows into title, owner, and chunked cell-content entries for global search.
func sheetsSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT id, title, content, modified_time, owners FROM sheets_spreadsheets`)
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
			"spreadsheet_title", "spreadsheet_owner", "spreadsheet_content", "spreadsheet_id")...)
	}
	return entries, nil
}
