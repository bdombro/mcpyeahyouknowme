package googlesheets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools adds all Google Sheets MCP tools to the server.
func (g *Source) RegisterTools(s *server.MCPServer) {
	prefix := g.Name() + "_"

	s.AddTool(mcp.NewTool(prefix+"search",
		mcp.WithDescription("Search across all Google Sheets"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return g.handleSearch(ctx, req)
	})

	s.AddTool(mcp.NewTool(prefix+"get_spreadsheet",
		mcp.WithDescription("Get full content of a specific Google Sheet by ID"),
		mcp.WithString("spreadsheet_id", mcp.Required(), mcp.Description("Google Spreadsheet ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return g.handleGetSpreadsheet(ctx, req)
	})

	s.AddTool(mcp.NewTool(prefix+"list_recent",
		mcp.WithDescription("List recently modified Google Sheets"),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return g.handleListRecent(ctx, req)
	})
}

func (g *Source) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	limit := core.IntArg(req.GetArguments(), "limit", 10)

	if g.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}

	rows, err := g.db.Query(`
		SELECT s.id, s.title, snippet(spreadsheets_fts, 1, '<mark>', '</mark>', '...', 32) as snippet,
		       s.modified_time, s.web_view_link, s.owners, s.sheet_count
		FROM spreadsheets_fts
		JOIN spreadsheets s ON s.rowid = spreadsheets_fts.rowid
		WHERE spreadsheets_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, title, snippet, modifiedTime, webViewLink, owners string
		var sheetCount int
		if err := rows.Scan(&id, &title, &snippet, &modifiedTime, &webViewLink, &owners, &sheetCount); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"title":         title,
			"snippet":       snippet,
			"modified_time": modifiedTime,
			"link":          webViewLink,
			"owners":        owners,
			"sheet_count":   sheetCount,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"query":   query,
		"results": results,
		"count":   len(results),
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}

func (g *Source) handleGetSpreadsheet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sheetID, _ := req.RequireString("spreadsheet_id")

	if g.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}

	var title, content, modifiedTime, webViewLink, owners string
	var sheetCount int
	err := g.db.QueryRow(`
		SELECT title, content, modified_time, web_view_link, owners, sheet_count
		FROM spreadsheets
		WHERE id = ?
	`, sheetID).Scan(&title, &content, &modifiedTime, &webViewLink, &owners, &sheetCount)

	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Spreadsheet not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve spreadsheet: %v", err)), nil
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"id":            sheetID,
		"title":         title,
		"content":       content,
		"modified_time": modifiedTime,
		"link":          webViewLink,
		"owners":        owners,
		"sheet_count":   sheetCount,
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}

func (g *Source) handleListRecent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 20)

	if g.db == nil {
		return mcp.NewToolResultText("{\"spreadsheets\":[],\"count\":0}"), nil
	}

	rows, err := g.db.Query(`
		SELECT id, title, modified_time, web_view_link, owners, sheet_count
		FROM spreadsheets
		ORDER BY modified_time DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list spreadsheets: %v", err)), nil
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, title, modifiedTime, webViewLink, owners string
		var sheetCount int
		if err := rows.Scan(&id, &title, &modifiedTime, &webViewLink, &owners, &sheetCount); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"title":         title,
			"modified_time": modifiedTime,
			"link":          webViewLink,
			"owners":        owners,
			"sheet_count":   sheetCount,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"spreadsheets": results,
		"count":        len(results),
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}
