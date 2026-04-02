package brave_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// Builds a test Brave client backed by an httptest server so client tests can inspect outgoing requests.
func newTestClient(t *testing.T, handler http.HandlerFunc) *BraveClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &BraveClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL + "/res/v1",
		apiKey:     "test-api-key",
	}
}

// Builds a test source backed by the httptest client so MCP tests exercise the same live-client path.
func newTestSource(t *testing.T, handler http.HandlerFunc) *Source {
	t.Helper()
	return &Source{client: newTestClient(t, handler)}
}

// Builds a minimal MCP server with the source tools registered so test helpers can invoke them directly.
func buildMCPServer(t *testing.T, src *Source) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	return s
}

// Invokes one MCP tool and returns its first text payload plus the tool-error flag for assertions.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) (string, bool) {
	t.Helper()

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	result := s.HandleMessage(context.Background(), msg)
	data, _ := json.Marshal(result)

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, string(data))
	}
	if len(resp.Result.Content) == 0 {
		return "", resp.Result.IsError
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}
