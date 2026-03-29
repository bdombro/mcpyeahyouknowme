package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func runMcp() {
	sources, err := LoadSources()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load data sources: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		for _, src := range sources {
			src.Close()
		}
	}()

	// Filter sources: only include WhatsApp if logged in
	var enabledSources []DataSource
	for _, src := range sources {
		if src.Name() == "whatsapp" {
			if !isLoggedIn() {
				fmt.Fprintf(os.Stderr, "Info: WhatsApp not logged in - WhatsApp MCP tools will not be available.\n")
				fmt.Fprintf(os.Stderr, "      Run 'mcpyeahyouknowme whatsapp login' to enable WhatsApp integration.\n")
				continue
			}
		}
		enabledSources = append(enabledSources, src)
	}

	// Initialize embedding model (nil if ONNX not installed)
	dir := dataDir()
	embedder, err := NewEmbedder(filepath.Join(dir, "models"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: embedding init failed: %v (falling back to BM25-only)\n", err)
		embedder = nil
	}
	if embedder == nil {
		fmt.Fprintf(os.Stderr, "Info: ONNX Runtime not found; semantic search disabled. Run ./tasks.sh install-onnx to enable.\n")
	}
	
	// TEMPORARY: Disable embeddings during indexing due to tokenizer library crashes
	// The BM25/FTS5 search will still work perfectly fine
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

	// Initialize global search index
	searchStore, err := NewSearchStore(dir, indexEmbedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: search index unavailable: %v\n", err)
	}
	if searchStore != nil {
		defer searchStore.Close()
		indexSources(searchStore, enabledSources)
		// Inject search store into sources that support vector-enhanced search
		for _, src := range enabledSources {
			if ws, ok := src.(*WhatsAppSource); ok {
				ws.SetSearchStore(searchStore)
			}
		}
	}

	s := server.NewMCPServer(
		"mcp-bridge",
		"1.0.0",
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
func indexSources(store *SearchStore, sources []DataSource) {
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

// registerSearchTool adds the global search MCP tool (not source-prefixed).
func registerSearchTool(s *server.MCPServer, store *SearchStore) {
	searchDesc := `Search across all connected data sources (WhatsApp, etc.) by name, participant, or message content. ` +
		`Returns results ranked by relevance using hybrid BM25 keyword + semantic vector search with hierarchy weighting ` +
		`(chat/contact names ranked highest, then participants, then message content).

Result metadata varies by source and content_type:
- WhatsApp chat_name: {"jid", "is_group"}
- WhatsApp participant: {"jid", "groups"} — use jid with whatsapp_get_chat or whatsapp_list_messages
- WhatsApp message: {"message_id", "chat_jid", "sender", "timestamp", "is_from_me"} — use message_id with whatsapp_get_message_context`

	s.AddTool(mcp.NewTool("search",
		mcp.WithDescription(searchDesc),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithString("source", mcp.Description("Filter to a specific source (e.g. 'whatsapp')")),
		mcp.WithString("content_type", mcp.Description("Filter by content type: 'chat_name', 'participant', or 'message'")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := req.RequireString("query")
		source, _ := args["source"].(string)
		contentType, _ := args["content_type"].(string)
		limit := intArg(args, "limit", 20)

		results, err := store.Search(query, limit, source, contentType)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(results)
	})
}

// ---------- Shared tool helpers ----------

func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func intArg(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

func boolArg(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}
