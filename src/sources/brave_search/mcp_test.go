package brave_search

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"mcpyeahyouknowme/core"
)

// Verifies the MCP web tool returns mapped search results on a successful upstream response.
func TestMCP_Web_success(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"query": {"original": "q", "more_results_available": false},
			"web": {"results": [{"title": "T", "url": "https://u", "description": "d"}]}
		}`)
	})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "brave_search_web", map[string]interface{}{
		"query": "q",
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var payload WebSearchPayload
	if err := core.UnmarshalToolResultTextPayload(text, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Query != "q" || len(payload.Results) != 1 || payload.Results[0].Title != "T" {
		t.Fatalf("payload: %+v", payload)
	}
}

// Verifies the MCP url tool returns exact_match when the result URL matches.
func TestMCP_URL_success_exact(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"query": {},
			"web": {"results": [{"title": "Page", "url": "https://example.com/x", "description": "d"}]}
		}`)
	})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "brave_search_get_meta", map[string]interface{}{
		"url": "https://example.com/x",
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var payload URLLookupPayload
	if err := core.UnmarshalToolResultTextPayload(text, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !payload.ExactMatch || payload.Result.Title != "Page" {
		t.Fatalf("payload: %+v", payload)
	}
}

// Verifies the MCP url tool returns exact_match false and the first hit when no result URL matches the request.
func TestMCP_URL_success_fallback(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"query": {},
			"web": {"results": [{"title": "Other", "url": "https://other.example/", "description": "x"}]}
		}`)
	})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "brave_search_get_meta", map[string]interface{}{
		"url": "https://target.example/page",
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var payload URLLookupPayload
	if err := core.UnmarshalToolResultTextPayload(text, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.ExactMatch || payload.Result.Title != "Other" || payload.Result.URL != "https://other.example/" {
		t.Fatalf("payload: %+v", payload)
	}
}

// Verifies MCP tools surface missing API-key configuration as tool errors.
func TestMCP_missingKey(t *testing.T) {
	src := &Source{client: &BraveClient{}}
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "brave_search_web", map[string]interface{}{
		"query": "q",
	})
	if !isErr {
		t.Fatalf("expected error result, got %q", text)
	}
	if !strings.Contains(text, "BRAVE_API_KEY") {
		t.Fatalf("unexpected error: %q", text)
	}
}

// Verifies upstream Brave failures propagate through the MCP tool as readable tool errors.
func TestMCP_upstreamError(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"message":"boom"}}`)
	})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "brave_search_get_meta", map[string]interface{}{
		"url": "https://example.com/",
	})
	if !isErr {
		t.Fatalf("expected error result, got %q", text)
	}
	if !strings.Contains(text, "boom") {
		t.Fatalf("unexpected error: %q", text)
	}
}

// Verifies required MCP arguments are validated before any upstream Brave API request is attempted.
func TestMCP_missingRequiredArgs(t *testing.T) {
	src := newTestSource(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("unexpected upstream call")
	})
	s := buildMCPServer(t, src)

	tests := []struct {
		name string
		tool string
		want string
	}{
		{"web", "brave_search_web", "query parameter is required"},
		{"get_meta", "brave_search_get_meta", "url parameter is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, s, tc.tool, map[string]interface{}{})
			if !isErr {
				t.Fatalf("expected error result, got %q", text)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("expected %q in %q", tc.want, text)
			}
		})
	}
}
