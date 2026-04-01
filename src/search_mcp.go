package main

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const searchToolDescription = `Search across all connected data sources (WhatsApp, Google Suite) by name, participant, or content. ` +
	`Returns results ranked by relevance using hybrid BM25 keyword + semantic vector search with hierarchy weighting.

Result metadata varies by source and content_type:
- WhatsApp chat_name: {"jid", "is_group"}
- WhatsApp participant: {"jid", "groups"} — use jid with whatsapp_get_chat
- WhatsApp message: {"message_id", "chat_jid", "sender", "timestamp"}
- Google Docs: {"document_id", "modified_time"} — use with gsuite_docs_get_document
- Google Sheets: {"spreadsheet_id", "modified_time"} — use with gsuite_sheets_get_spreadsheet
- Gmail: {"message_id", "from", "date", "folder"} — use with gsuite_gmail_get_message
- Calendar: {"event_id", "start_time", "end_time"} — use with gsuite_calendar_get_event
- Tasks: {"task_id", "status", "due"} — use with gsuite_tasks_search
- Contacts: {"resource_name", "emails", "phones"} — use with gsuite_contacts_search
- Slides: {"presentation_id", "modified_time"} — use with gsuite_slides_get_presentation`

type searchToolStore interface {
	Search(query string, limit int, sourceFilter, typeFilter string) ([]SearchResult, error)
}

// RegisterSearchTool adds the global search MCP tool.
func RegisterSearchTool(s *server.MCPServer, store searchToolStore) {
	s.AddTool(mcp.NewTool("search",
		mcp.WithDescription(searchToolDescription),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithString("source", mcp.Description("Filter to a specific source (e.g. 'whatsapp')")),
		mcp.WithString("content_type", mcp.Description("Filter by content type: 'chat_name', 'participant', or 'message'")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query parameter is required"), nil
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
