package main

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// searchToolDescription tells callers that the search engine is BM25 keyword-based
// and guides LLMs to expand queries before calling.
const searchToolDescription = `Search personal data (Gmail, WhatsApp, Docs, Calendar, Notes, Browser) via BM25 keyword search. ` +
	`Before calling: extract 2–4 core keywords from the question, drop filler words, and include synonyms for better recall (e.g. "invoice bill payment"). ` +
	`If results are empty, retry with different or fewer keywords.`

const searchToolExample = `{"query":"birthday dinner 2024"}`

// noResultsHint is returned instead of an empty array so callers receive actionable
// retry guidance rather than a silent empty response.
const noResultsHint = `No matches found. BM25 requires exact word overlap. ` +
	`Tips: (1) extract 2–4 core keywords, drop filler words; ` +
	`(2) add synonyms or alternate phrasing (e.g. "invoice bill payment"); ` +
	`(3) try a shorter or different query angle.`

type searchToolStore interface {
	Search(query string, limit int, sourceFilter, typeFilter string) ([]SearchResult, error)
}

// RegisterSearchTool centralizes global-search MCP wiring so startup can expose one BM25 search entrypoint backed by `store`.
func RegisterSearchTool(s *server.MCPServer, store searchToolStore) {
	s.AddTool(core.NewReadOnlyTool("search",
		core.ToolDescription(searchToolDescription, searchToolExample),
		mcp.WithString("query", mcp.Required(), mcp.Description("Keywords extracted from the question. Include synonyms for better recall (e.g. 'invoice bill payment').")),
		mcp.WithString("source", mcp.Description("Filter to a specific source (e.g. 'whatsapp')")),
		mcp.WithString("content_type", mcp.Description("Filter by content type (e.g. 'chat_name', 'chat_content', 'document_title', 'email_thread_subject', 'note_content', 'browser_visit')")),
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
		if len(results) == 0 {
			return mcp.NewToolResultText(noResultsHint), nil
		}
		return core.JsonResult(results)
	})
}
