package google_places

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMCP_SearchPlaces_success(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
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
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(results) != 1 || results[0].PlaceID != "place-1" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestMCP_GetPlace_success(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
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
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.PlaceID != "place-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

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

func TestMCP_upstreamError(t *testing.T) {
	src := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
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
