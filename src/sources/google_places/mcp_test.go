package google_places

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"mcpyeahyouknowme/core"
)

// Verifies the MCP search tool returns mapped Places summaries on a successful upstream response.
func TestMCP_SearchPlaces_success(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"places": [{
				"id": "place-1",
				"displayName": {"text": "Cafe"},
				"formattedAddress": "123 Main St",
				"types": ["cafe"],
				"location": {"latitude": 1.2, "longitude": 3.4},
				"businessStatus": "OPERATIONAL"
			}]
		}`)
	})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "google_places_search_places", map[string]interface{}{
		"query":       "cafe",
		"max_results": 2,
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}

	var results []PlaceSummary
	if err := core.UnmarshalToolResultTextPayload(text, &results); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(results) != 1 || results[0].PlaceID != "place-1" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

// Verifies the MCP details tool returns mapped place details on a successful upstream response.
func TestMCP_GetPlace_success(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"id": "place-1",
			"displayName": {"text": "Cafe"},
			"formattedAddress": "123 Main St"
		}`)
	})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "google_places_get_place", map[string]interface{}{
		"place_id": "place-1",
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}

	var result PlaceDetails
	if err := core.UnmarshalToolResultTextPayload(text, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.PlaceID != "place-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// Verifies the MCP tools surface missing API-key configuration as tool errors instead of empty success payloads.
func TestMCP_missingKey(t *testing.T) {
	src := &Source{client: &PlacesClient{}}
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "google_places_search_places", map[string]interface{}{
		"query": "cafe",
	})
	if !isErr {
		t.Fatalf("expected error result, got %q", text)
	}
	if !strings.Contains(text, "GOOGLE_PLACE_API_KEY") {
		t.Fatalf("unexpected error: %q", text)
	}
}

// Verifies upstream Places API failures propagate through the MCP tool as readable tool errors.
func TestMCP_upstreamError(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"message":"backend exploded"}}`)
	})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "google_places_get_place", map[string]interface{}{
		"place_id": "place-1",
	})
	if !isErr {
		t.Fatalf("expected error result, got %q", text)
	}
	if !strings.Contains(text, "backend exploded") {
		t.Fatalf("unexpected error: %q", text)
	}
}

// Verifies required MCP arguments are validated before any upstream Places API request is attempted.
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
		{"search_places", "google_places_search_places", "query parameter is required"},
		{"get_place", "google_places_get_place", "place_id parameter is required"},
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
