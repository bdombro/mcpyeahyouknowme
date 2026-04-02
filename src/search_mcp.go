package main

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const searchToolDescription = `Search across all connected data sources by name, participant, or content. ` +
	`Requires query; source, content_type, and limit are optional. Returns results ranked by hybrid BM25 + semantic vector search.`

const searchToolExample = `{"query":"meeting notes"}`

type searchToolStore interface {
	Search(query string, limit int, sourceFilter, typeFilter string) ([]SearchResult, error)
}

// RegisterSearchTool centralizes global-search MCP wiring so startup can expose one hybrid search entrypoint backed by `store`.
func RegisterSearchTool(s *server.MCPServer, store searchToolStore) {
	s.AddTool(core.NewReadOnlyTool("search",
		core.ToolDescription(searchToolDescription, searchToolExample),
		mcp.WithString("query", mcp.Required(), mcp.Description("Required search query")),
		mcp.WithString("source", mcp.Description("Filter to a specific source (e.g. 'whatsapp')")),
		mcp.WithString("content_type", mcp.Description("Filter by content type: 'chat_name', 'participant', or 'message'")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 20)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, errResult := core.RequireStringArgument(req, "query", searchToolExample)
		if errResult != nil {
			return errResult, nil
		}
		source, _ := args["source"].(string)
		contentType, _ := args["content_type"].(string)
		limit := core.IntArg(args, "limit", 20)

		results, err := store.Search(query, limit, source, contentType)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(results)
	})
}
