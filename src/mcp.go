package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/whatsapp"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func runMcp() {
	dir := core.DataDir()
	sources := LoadSources(dir)
	defer func() {
		for _, src := range sources {
			src.Close()
		}
	}()

	// Filter sources: only include WhatsApp if logged in
	var enabledSources []core.DataSource
	for _, src := range sources {
		if src.Name() == "whatsapp" {
			if !whatsapp.IsLoggedIn(dir) {
				fmt.Fprintf(os.Stderr, "Info: WhatsApp not logged in - WhatsApp MCP tools will not be available.\n")
				fmt.Fprintf(os.Stderr, "      Run 'mcpyeahyouknowme whatsapp login' to enable WhatsApp integration.\n")
				continue
			}
		}
		enabledSources = append(enabledSources, src)
	}

	embedder, err := NewEmbedder(filepath.Join(dir, "models"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: embedding init failed: %v (falling back to BM25-only)\n", err)
		embedder = nil
	}
	if embedder == nil {
		fmt.Fprintf(os.Stderr, "Info: ONNX Runtime not found; semantic search disabled. Run 'brew install onnxruntime' to enable.\n")
	}

	var indexEmbedder EmbedderInterface
	if os.Getenv("MCP_ENABLE_EMBEDDINGS") == "1" {
		indexEmbedder = embedder
	} else {
		if embedder != nil {
			fmt.Fprintf(os.Stderr, "Info: Embeddings disabled during indexing (use MCP_ENABLE_EMBEDDINGS=1 to enable)\n")
		}
		indexEmbedder = nil
	}
	if embedder != nil {
		defer embedder.Close()
	}

	searchStore, err := NewSearchStore(dir, indexEmbedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: search index unavailable: %v\n", err)
	}
	if searchStore != nil {
		defer searchStore.Close()
		indexSources(searchStore, enabledSources)
	}

	s := server.NewMCPServer(
		"mcpyeahyouknowme",
		BuildVersion,
		server.WithToolCapabilities(false),
	)

	for _, src := range enabledSources {
		src.RegisterTools(s)
	}

	if searchStore != nil {
		registerSearchTool(s, searchStore)
	}

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

// indexSources populates the search index from all data sources.
func indexSources(store *SearchStore, sources []core.DataSource) {
	for _, src := range sources {
		entries, err := src.SearchEntries()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get search entries from %s: %v\n", src.Name(), err)
			continue
		}
		if err := store.IndexEntries(entries); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to index %s entries: %v\n", src.Name(), err)
			continue
		}
		store.UpdateSourceTimestamp(src.Name(), time.Now())
	}
}

// registerSearchTool adds the global search MCP tool.
func registerSearchTool(s *server.MCPServer, store *SearchStore) {
	searchDesc := `Search across all connected data sources (WhatsApp, Google Docs, Google Sheets, etc.) by name, participant, or message content. ` +
		`Returns results ranked by relevance using hybrid BM25 keyword + semantic vector search with hierarchy weighting ` +
		`(chat/contact names ranked highest, then participants, then message content).

Result metadata varies by source and content_type:
- WhatsApp chat_name: {"jid", "is_group"}
- WhatsApp participant: {"jid", "groups"} — use jid with whatsapp_get_chat or whatsapp_list_messages
- WhatsApp message: {"message_id", "chat_jid", "sender", "timestamp", "is_from_me"} — use message_id with whatsapp_get_message_context
- Google Docs document_title/content: {"document_id", "modified_time"} — use document_id with googledocs_get_document
- Google Sheets spreadsheet_title/content: {"spreadsheet_id", "modified_time"} — use spreadsheet_id with googlesheets_get_spreadsheet`

	s.AddTool(mcp.NewTool("search",
		mcp.WithDescription(searchDesc),
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
