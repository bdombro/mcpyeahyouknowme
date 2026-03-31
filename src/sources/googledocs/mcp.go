package googledocs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools adds all Google Docs MCP tools to the server.
func (g *Source) RegisterTools(s *server.MCPServer) {
	prefix := g.Name() + "_"

	s.AddTool(mcp.NewTool(prefix+"search",
		mcp.WithDescription("Search across all Google Docs"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return g.handleSearch(ctx, req)
	})

	s.AddTool(mcp.NewTool(prefix+"get_document",
		mcp.WithDescription("Get full content of a specific Google Doc by ID"),
		mcp.WithString("document_id", mcp.Required(), mcp.Description("Google Doc ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return g.handleGetDocument(ctx, req)
	})

	s.AddTool(mcp.NewTool(prefix+"list_recent",
		mcp.WithDescription("List recently modified Google Docs"),
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
		SELECT d.id, d.title, snippet(documents_fts, 1, '<mark>', '</mark>', '...', 32) as snippet, 
		       d.modified_time, d.web_view_link, d.owners
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
		var id, title, snippet, modifiedTime, webViewLink, owners string
		if err := rows.Scan(&id, &title, &snippet, &modifiedTime, &webViewLink, &owners); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"title":         title,
			"snippet":       snippet,
			"modified_time": modifiedTime,
			"link":          webViewLink,
			"owners":        owners,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"query":   query,
		"results": results,
		"count":   len(results),
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}

func (g *Source) handleGetDocument(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	docID, _ := req.RequireString("document_id")

	if g.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}

	var title, content, modifiedTime, webViewLink, owners string
	err := g.db.QueryRow(`
		SELECT title, content, modified_time, web_view_link, owners
		FROM documents
		WHERE id = ?
	`, docID).Scan(&title, &content, &modifiedTime, &webViewLink, &owners)

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
		"owners":        owners,
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}

func (g *Source) handleListRecent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 20)

	if g.db == nil {
		return mcp.NewToolResultText("{\"documents\":[],\"count\":0}"), nil
	}

	rows, err := g.db.Query(`
		SELECT id, title, modified_time, web_view_link, owners
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
		var id, title, modifiedTime, webViewLink, owners string
		if err := rows.Scan(&id, &title, &modifiedTime, &webViewLink, &owners); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"title":         title,
			"modified_time": modifiedTime,
			"link":          webViewLink,
			"owners":        owners,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"documents": results,
		"count":     len(results),
	}, "", "  ")

	return mcp.NewToolResultText(string(data)), nil
}
