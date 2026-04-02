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
	_ "github.com/mattn/go-sqlite3"
	"mcpyeahyouknowme/sources/whatsapp"
)

type failingSearchStore struct{}

func (failingSearchStore) Search(_ string, _ int, _, _ string) ([]SearchResult, error) {
	return nil, errors.New("search failed")
}

// buildTestMCPServerWithSearch creates an MCP server with the global search tool,
// seeded with WhatsApp fixture data.
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

	searchDB, err := sql.Open("sqlite3", "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open search db: %v", err)
	}
	t.Cleanup(func() { searchDB.Close() })
	searchDB.Exec("PRAGMA journal_mode=WAL")
	searchDB.Exec("PRAGMA busy_timeout=5000")

	emb := &mockEmbedder{dim: 16}
	searchStore, err := NewSearchStoreFromDB(searchDB, emb)
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

func seedSearchFixtures(t *testing.T, store *whatsapp.MessageStore) {
	t.Helper()
	now := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice Smith", now.Add(-1*time.Hour))
	store.StoreChat("group1@g.us", "Family Chat", now.Add(-30*time.Minute))
	store.StoreMessage("m1", "11111@s.whatsapp.net", "11111", "Hello Alice", now.Add(-1*time.Hour), false, "", "", "", nil, nil, nil, 0)
	store.StoreMessage("m4", "group1@g.us", "11111", "Family dinner tonight", now.Add(-30*time.Minute), false, "", "", "", nil, nil, nil, 0)
}

func callSearchTool(t *testing.T, s *server.MCPServer, args map[string]interface{}) string {
	t.Helper()
	return callGlobalTool(t, s, "search", args)
}

func callGlobalTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) string {
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
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	result := s.HandleMessage(ctx, msg)
	data, _ := json.Marshal(result)

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	json.Unmarshal(data, &resp)
	if len(resp.Result.Content) == 0 {
		return ""
	}
	return resp.Result.Content[0].Text
}

func TestMCP_GlobalSearch_basic(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family"})
	if text == "" {
		t.Error("expected non-empty search result")
	}
}

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

func TestMCP_GlobalSearch_messageContent(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "dinner"})
	if text == "" || text == "[]" {
		t.Error("expected search results for 'dinner'")
	}
}

func TestMCP_GlobalSearch_participant(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Alice"})
	if text == "" || text == "[]" {
		t.Error("expected search results for 'Alice'")
	}
}

func TestMCP_GlobalSearch_sourceFilter(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family", "source": "whatsapp"})
	if text == "" || text == "[]" {
		t.Error("expected results for whatsapp source filter")
	}
}

func TestMCP_GlobalSearch_typeFilter(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family", "content_type": "chat_name"})
	if text == "" || text == "[]" {
		t.Error("expected results for chat_name type filter")
	}
}

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

	searchDB, _ := sql.Open("sqlite3", "file::memory:?cache=shared&_foreign_keys=on")
	defer searchDB.Close()

	searchStore, _ := NewSearchStoreFromDB(searchDB, nil)
	entries, _ := ws.SearchEntries()
	searchStore.IndexEntries(entries)

	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	ws.RegisterTools(s)
	RegisterSearchTool(s, searchStore)

	text := callGlobalTool(t, s, "search", map[string]interface{}{"query": "zzzznonexistent"})
	if text != "[]" {
		t.Errorf("expected empty results, got %q", text)
	}
}

func TestMCP_GlobalSearch_withLimit(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{"query": "Family", "limit": float64(1)})
	var results []SearchResult
	json.Unmarshal([]byte(text), &results)
	if len(results) > 1 {
		t.Errorf("expected at most 1 result with limit=1, got %d", len(results))
	}
}

func TestMCP_GlobalSearch_missingQuery(t *testing.T) {
	s := buildTestMCPServerWithSearch(t)
	text := callSearchTool(t, s, map[string]interface{}{})
	want := `query parameter is required; call with arguments: {"query":"meeting notes","source":"whatsapp","limit":5}`
	if text != want {
		t.Errorf("expected missing query error, got %q", text)
	}
}

func TestMCP_GlobalSearch_storeError(t *testing.T) {
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	RegisterSearchTool(s, failingSearchStore{})

	text := callGlobalTool(t, s, "search", map[string]interface{}{"query": "Family"})
	if !strings.Contains(text, "search failed") {
		t.Errorf("expected database error, got %q", text)
	}
}

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
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
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
			break
		}
	}
	if !found {
		t.Error("expected 'search' tool in tools list")
	}
}
