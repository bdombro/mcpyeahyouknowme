package brave_search

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const webExample = `{"query":"golang sqlite orm","count":10,"offset":0}`

const urlExample = `{"url":"https://pkg.go.dev/database/sql"}`

// registerTools registers read-only Brave Search tools under the source prefix, surfacing network/API failures as tool errors.
func registerTools(src *Source, s *server.MCPServer) {
	prefix := src.Name() + "_"

	s.AddTool(core.NewReadOnlyTool(prefix+"web",
		core.ToolDescription("Search the public web for current information not available in local data sources. Use for real-time lookups, documentation, news, or verifying facts.", webExample),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query text")),
		mcp.WithNumber("count", mcp.Description("Max results per page (default 20, max 20)")),
		mcp.WithNumber("offset", mcp.Description("Result page offset (0-based, max 9)")),
		mcp.WithString("country", mcp.Description("Two-letter country code")),
		mcp.WithString("search_lang", mcp.Description("Search language (ISO 639-1)")),
		mcp.WithString("ui_lang", mcp.Description("UI language for response metadata")),
		mcp.WithString("safesearch", mcp.Description("Safe search: off, moderate, or strict")),
		mcp.WithString("freshness", mcp.Description("Freshness filter (e.g. pd, pw, pm, py, or custom range)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, errResult := core.RequireStringArgument(req, "query", webExample)
		if errResult != nil {
			return errResult, nil
		}
		args := req.GetArguments()
		opts := WebSearchOptions{
			Query:      query,
			Count:      core.IntArg(args, "count", 20),
			Offset:     core.IntArg(args, "offset", 0),
			Country:    core.StringArg(args, "country"),
			SearchLang: core.StringArg(args, "search_lang"),
			UILang:     core.StringArg(args, "ui_lang"),
			SafeSearch: core.StringArg(args, "safesearch"),
			Freshness:  core.StringArg(args, "freshness"),
		}
		payload, err := src.client.WebSearch(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(payload)
	})

	s.AddTool(core.NewReadOnlyTool(prefix+"get_meta",
		core.ToolDescription("Fast metadata lookup for a URL — returns page title and description from Brave's search index without fetching the page itself. Much faster than loading the full page when you only need metadata. Prefer this over full page fetch tools when title and description are sufficient.", urlExample),
		mcp.WithString("url", mcp.Required(), mcp.Description("Page URL to look up")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		pageURL, errResult := core.RequireStringArgument(req, "url", urlExample)
		if errResult != nil {
			return errResult, nil
		}
		payload, err := src.client.LookupURL(ctx, pageURL)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(payload)
	})
}
