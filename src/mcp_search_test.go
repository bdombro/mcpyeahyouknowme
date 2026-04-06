package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"mcpyeahyouknowme/sources/whatsapp"
	_ "modernc.org/sqlite"
)

type failingSearchStore struct{}

// Returns a fixed error so global-search MCP tests can verify tool error propagation.
func (failingSearchStore) Search(_ string, _ int, _, _ string) ([]SearchResult, error) {
	return nil, errors.New("search failed")
}

// Builds a test MCP server with seeded WhatsApp data and the global search tool wired against an in-memory search store.
func buildTestMCPServerWithSearch(t *testing.T) *server.MCPServer {
	t.Helper()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	t.Cleanup(apiSrv.Close)

	store, err := whatsapp.NewMessageStore(t.TempDir())
	if err != nil {
		t.Fatalf("create message store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	seedSearchFixtures(t, store)
	ws := whatsapp.NewSourceFromStore(store, apiSrv.URL)

	searchDB, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open search db: %v", err)
	}
	t.Cleanup(func() { searchDB.Close() })
	searchDB.Exec("PRAGMA journal_mode=WAL")
	searchDB.Exec("PRAGMA busy_timeout=5000")

	searchStore, err := NewSearchStoreFromDB(searchDB)
	if err != nil {
		t.Fatalf("create search store: %v", err)
	}

	entries, _ := ws.SearchEntries()
	searchStore.IndexEntries(entries)

	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	ws.RegisterTools(s)
	RegisterSearchTool(s, searchStore)
	return s
}

// Seeds minimal WhatsApp fixtures so global-search MCP tests have chat, participant, and message content to query.
func seedSearchFixtures(t *testing.T, store *whatsapp.MessageStore) {
	t.Helper()
	now := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice Smith", now.Add(-1*time.Hour))
	store.StoreChat("group1@g.us", "Family Chat", now.Add(-30*time.Minute))
	store.StoreMessage("m1", "11111@s.whatsapp.net", "11111", "Hello Alice", now.Add(-1*time.Hour), false, "", "", "", nil, nil, nil, 0)
	store.StoreMessage("m4", "group1@g.us", "11111", "Family dinner tonight", now.Add(-30*time.Minute), false, "", "", "", nil, nil, nil, 0)
}

// Invokes the global search MCP tool with args and returns the first text payload for assertions.
func callSearchTool(t *testing.T, s *server.MCPServer, args map[string]interface{}) string {
	t.Helper()
	return callGlobalTool(t, s, "search", args)
}

// Invokes the global search MCP tool without an arguments object so tests can pin framework behavior for omitted params.arguments.
func callSearchToolWithoutArguments(t *testing.T, s *server.MCPServer) string {
	t.Helper()
	return callGlobalToolWithParams(t, s, map[string]interface{}{"name": "search"})
}

// Invokes a named MCP tool against an initialized test server and returns the first text payload.
func callGlobalTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) string {
	t.Helper()
	return callGlobalToolWithParams(t, s, map[string]interface{}{"name": name, "arguments": args})
}

// Invokes a named MCP tool against an initialized test server using raw params so tests can cover missing arguments payloads.
func callGlobalToolWithParams(t *testing.T, s *server.MCPServer, params map[string]interface{}) string {
	t.Helper()
	ctx := context.Background()

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(ctx, initMsg)

	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": params,
	})
	result := s.HandleMessage(ctx, msg)
	data, _ := json.Marshal(result)

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(data, &resp)
	if len(resp.Result.Content) == 0 {
		if resp.Error != nil {
			return resp.Error.Message
		}
		return ""
	}
	return resp.Result.Content[0].Text
}

// Verifies the global search tool returns non-empty results for a basic query over seeded fixtures.
func TestMCP_GlobalSearch_basic(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family"})
	if text == "" {
		t.Error("expected non-empty search result")
	}
}

// Verifies global search results include follow-up metadata hints for downstream tool navigation.
func TestMCP_GlobalSearch_metadataHint(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family"})
	var results []SearchResult
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("unmarshal search results: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}
	if results[0].MetadataHint == "" {
		t.Fatal("expected metadata_hint on search result")
	}
}

// Verifies global search can return WhatsApp chat-transcript hits, not just title-style matches.
func TestMCP_GlobalSearch_chatContent(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "dinner"})
	var results []SearchResult
	if err := json.Unmarshal([]byte(text), &results); err != nil || len(results) == 0 {
		t.Errorf("expected search results for 'dinner', got %q", text)
	}
}

// Verifies global search can return participant-style hits for contact-name queries.
func TestMCP_GlobalSearch_participant(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Alice"})
	var results []SearchResult
	if err := json.Unmarshal([]byte(text), &results); err != nil || len(results) == 0 {
		t.Errorf("expected search results for 'Alice', got %q", text)
	}
}

// Verifies the source filter narrows global search results to the requested source.
func TestMCP_GlobalSearch_sourceFilter(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family", "source": "whatsapp"})
	var results []SearchResult
	if err := json.Unmarshal([]byte(text), &results); err != nil || len(results) == 0 {
		t.Errorf("expected results for whatsapp source filter, got %q", text)
	}
}

// Verifies the content-type filter narrows global search results to the requested indexed type.
func TestMCP_GlobalSearch_typeFilter(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family", "content_type": "chat_name"})
	var results []SearchResult
	if err := json.Unmarshal([]byte(text), &results); err != nil || len(results) == 0 {
		t.Errorf("expected results for chat_name type filter, got %q", text)
	}
}

// Verifies the content-type filter can target WhatsApp chat transcript chunks directly.
func TestMCP_GlobalSearch_chatContentTypeFilter(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "dinner", "content_type": "chat_content"})
	var results []SearchResult
	if err := json.Unmarshal([]byte(text), &results); err != nil || len(results) == 0 {
		t.Errorf("expected results for chat_content type filter, got %q", text)
	}
}

// Verifies that a query with no keyword matches returns the actionable retry hint instead of a silent empty array.
func TestMCP_GlobalSearch_noKeywordMatch(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer apiSrv.Close()

	store, _ := whatsapp.NewMessageStore(t.TempDir())
	defer store.Close()
	seedSearchFixtures(t, store)
	ws := whatsapp.NewSourceFromStore(store, apiSrv.URL)

	searchDB, _ := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	defer searchDB.Close()

	searchStore, _ := NewSearchStoreFromDB(searchDB)
	entries, _ := ws.SearchEntries()
	searchStore.IndexEntries(entries)

	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	ws.RegisterTools(s)
	RegisterSearchTool(s, searchStore)

	text := callGlobalTool(t, s, "search", map[string]interface{}{"query": "zzzznonexistent"})
	if !strings.Contains(text, "No matches found") {
		t.Errorf("expected no-results hint, got %q", text)
	}
}

// Verifies the global search tool honors the caller-provided result limit.
func TestMCP_GlobalSearch_withLimit(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family", "limit": float64(1)})
	var results []SearchResult
	json.Unmarshal([]byte(text), &results)
	if len(results) > 1 {
		t.Errorf("expected at most 1 result with limit=1, got %d", len(results))
	}
}

// Verifies the global search tool returns the expected retry-oriented error when `query` is missing.
func TestMCP_GlobalSearch_missingQuery(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{})
	want := `query parameter is required; retry with params.arguments: {"query":"birthday dinner 2024"}`
	if text != want {
		t.Errorf("expected missing query error, got %q", text)
	}
}

// Verifies omitting params.arguments still returns the same retry-oriented required-query guidance.
func TestMCP_GlobalSearch_missingArgumentsObject(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchToolWithoutArguments(t, s)
	want := `query parameter is required; retry with params.arguments: {"query":"birthday dinner 2024"}`
	if text != want {
		t.Errorf("expected missing query error, got %q", text)
	}
}

// Verifies store-layer search failures surface as MCP tool errors instead of empty success payloads.
func TestMCP_GlobalSearch_storeError(t *testing.T) {
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	RegisterSearchTool(s, failingSearchStore{})

	text := callGlobalTool(t, s, "search", map[string]interface{}{"query": "Family"})
	if !strings.Contains(text, "search failed") {
		t.Errorf("expected database error, got %q", text)
	}
}

// Verifies tools/list includes the global search tool with the expected schema and safety annotations.
func TestMCP_ToolsListContainsSearchTool(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	ctx := context.Background()

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(ctx, initMsg)

	listMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	resultIface := s.HandleMessage(ctx, listMsg)
	result, _ := json.Marshal(resultIface)

	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnlyHint    *bool `json:"readOnlyHint"`
					DestructiveHint *bool `json:"destructiveHint"`
					IdempotentHint  *bool `json:"idempotentHint"`
				} `json:"annotations"`
				InputSchema struct {
					Description string                     `json:"description"`
					Properties  map[string]json.RawMessage `json:"properties"`
					Required    []string                   `json:"required"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	json.Unmarshal(result, &resp)

	found := false
	for _, tool := range resp.Result.Tools {
		if tool.Name == "search" {
			found = true
			if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
				t.Fatal("expected search readOnlyHint=true")
			}
			if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
				t.Fatal("expected search destructiveHint=false")
			}
			if tool.Annotations.IdempotentHint == nil || !*tool.Annotations.IdempotentHint {
				t.Fatal("expected search idempotentHint=true")
			}
			if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "query" {
				t.Fatalf("unexpected required fields: %#v", tool.InputSchema.Required)
			}
			for _, key := range []string{"query", "source", "content_type", "limit"} {
				if _, ok := tool.InputSchema.Properties[key]; !ok {
					t.Fatalf("expected %q in inputSchema.properties", key)
				}
			}
			var querySchema struct {
				Description string `json:"description"`
			}
			if err := json.Unmarshal(tool.InputSchema.Properties["query"], &querySchema); err != nil {
				t.Fatalf("unmarshal query schema: %v", err)
			}
			if !strings.Contains(querySchema.Description, "synonyms") {
				t.Fatalf("expected query description to mention synonyms, got %q", querySchema.Description)
			}
			break
		}
	}
	if !found {
		t.Error("expected 'search' tool in tools list")
	}
}
