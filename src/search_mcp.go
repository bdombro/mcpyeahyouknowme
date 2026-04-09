package main

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
)

// searchToolDescription tells callers that the search engine is BM25 keyword-based
// and guides LLMs to expand queries before calling.
const searchToolDescription = `Search personal data (Gmail, WhatsApp, Docs, Calendar, Notes, Browser) via BM25 keyword search. ` +
	`Before calling: extract 2–4 core keywords from the question, drop filler words, and include synonyms for better recall (e.g. "invoice bill payment"). ` +
	`If results are empty, retry with different or fewer keywords. ` +
	`Use "after"/"before" (RFC3339) to scope results to a date range when the question implies one.`

const searchToolExample = `{"query":"birthday dinner 2024","after":"2024-01-01T00:00:00Z","before":"2025-01-01T00:00:00Z"}`

// noResultsHint is returned instead of an empty array so callers receive actionable
// retry guidance rather than a silent empty response.
const noResultsHint = `No matches found. BM25 requires exact word overlap. ` +
	`Tips: (1) extract 2–4 core keywords, drop filler words; ` +
	`(2) add synonyms or alternate phrasing (e.g. "invoice bill payment"); ` +
	`(3) try a shorter or different query angle.`

type searchToolStore interface {
	Search(query string, limit int, sourceFilter, typeFilter, after, before string) ([]SearchResult, error)
}

// searchResultSourceMayCarryInjection reports whether indexed rows from this source can contain untrusted external text.
func searchResultSourceMayCarryInjection(source string) bool {
	switch source {
	case "whatsapp", "gsuite", "browser_history", "notebook":
		return true
	default:
		return false
	}
}

// searchResultsNeedUntrustedWarning is true when any hit may include attacker-controlled content worth flagging to the model.
func searchResultsNeedUntrustedWarning(results []SearchResult) bool {
	for _, r := range results {
		if searchResultSourceMayCarryInjection(r.Source) {
			return true
		}
	}
	return false
}

// RegisterSearchTool centralizes global-search MCP wiring so startup can expose one BM25 search entrypoint backed by `store`.
func RegisterSearchTool(s core.ToolAdder, store searchToolStore) {
	s.AddTool(core.NewReadOnlyTool("search",
		core.ToolDescription(searchToolDescription, searchToolExample),
		mcp.WithString("query", mcp.Required(), mcp.Description("Keywords extracted from the question. Include synonyms for better recall (e.g. 'invoice bill payment').")),
		mcp.WithString("source", mcp.Description("Filter to a specific source (e.g. 'whatsapp')")),
		mcp.WithString("content_type", mcp.Description("Filter by content type. Values: chat_name, chat_content, participant, document_title, document_content, spreadsheet_title, spreadsheet_content, email_thread_subject, email_thread_participants, email_thread_content, calendar_event, calendar_event_description, task, contact, presentation_title, presentation_content, note_title, note_content, pdf_title, pdf_content, image, browser_visit")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 20)")),
		mcp.WithString("after", mcp.Description("Return only entries with timestamp >= this RFC3339 value (e.g. '2024-01-01T00:00:00Z')")),
		mcp.WithString("before", mcp.Description("Return only entries with timestamp <= this RFC3339 value (e.g. '2025-01-01T00:00:00Z')")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, errResult := core.RequireStringArgument(req, "query", searchToolExample)
		if errResult != nil {
			return errResult, nil
		}
		source, _ := args["source"].(string)
		contentType, _ := args["content_type"].(string)
		after, _ := args["after"].(string)
		before, _ := args["before"].(string)
		limit := core.IntArg(args, "limit", 20)

		results, err := store.Search(query, limit, source, contentType, after, before)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(results) == 0 {
			return mcp.NewToolResultText(noResultsHint), nil
		}
		if searchResultsNeedUntrustedWarning(results) {
			return core.UntrustedJSONResult(results, "search")
		}
		return core.JsonResult(results)
	})
}
