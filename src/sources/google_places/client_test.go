package google_places

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSearchPlaces_success(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/places:searchText" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != "test-api-key" {
			t.Fatalf("api key header = %q", got)
		}
		if got := r.Header.Get("X-Goog-FieldMask"); got != searchFieldMask {
			t.Fatalf("field mask = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var req searchTextRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.TextQuery != "coffee" || req.PageSize != 3 {
			t.Fatalf("unexpected request: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"places": [{
				"id": "place-1",
				"displayName": {"text": "Blue Bottle Coffee"},
				"formattedAddress": "300 Webster St, Oakland, CA",
				"types": ["cafe", "food"],
				"location": {"latitude": 37.796, "longitude": -122.276},
				"businessStatus": "OPERATIONAL"
			}]
		}`)
	})

	results, err := client.SearchPlaces(context.Background(), "coffee", 3)
	if err != nil {
		t.Fatalf("SearchPlaces(): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].PlaceID != "place-1" || results[0].DisplayName != "Blue Bottle Coffee" {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	if results[0].Location == nil || results[0].Location.Latitude != 37.796 {
		t.Fatalf("unexpected location: %+v", results[0].Location)
	}
}

func TestSearchPlaces_errorPaths(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		client := &PlacesClient{}
		_, err := client.SearchPlaces(context.Background(), "coffee", 1)
		if err == nil || !strings.Contains(err.Error(), "GOOGLE_PLACE_API_KEY") {
			t.Fatalf("err = %v, want missing key", err)
		}
	})

	t.Run("http error", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"message":"permission denied"}}`)
		})
		_, err := client.SearchPlaces(context.Background(), "coffee", 1)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("err = %v, want permission denied", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"places":[`)
		})
		_, err := client.SearchPlaces(context.Background(), "coffee", 1)
		if err == nil || !strings.Contains(err.Error(), "decode Google Places response") {
			t.Fatalf("err = %v, want decode error", err)
		}
	})
}

func TestGetPlace_success(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/places/place-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Goog-FieldMask"); got != detailsFieldMask {
			t.Fatalf("field mask = %q", got)
		}
		io.WriteString(w, `{
			"id": "place-1",
			"displayName": {"text": "Blue Bottle Coffee"},
			"formattedAddress": "300 Webster St, Oakland, CA",
			"nationalPhoneNumber": "(510) 653-3394",
			"internationalPhoneNumber": "+1 510-653-3394",
			"websiteUri": "https://example.com",
			"googleMapsUri": "https://maps.google.com/?cid=1",
			"location": {"latitude": 37.796, "longitude": -122.276},
			"regularOpeningHours": {
				"openNow": true,
				"weekdayDescriptions": ["Monday: 7:00 AM – 6:00 PM"]
			},
			"rating": 4.6,
			"userRatingCount": 321,
			"addressComponents": [{
				"longText": "300",
				"shortText": "300",
				"types": ["street_number"]
			}],
			"businessStatus": "OPERATIONAL",
			"types": ["cafe"],
			"priceLevel": "PRICE_LEVEL_MODERATE"
		}`)
	})

	result, err := client.GetPlace(context.Background(), "place-1")
	if err != nil {
		t.Fatalf("GetPlace(): %v", err)
	}
	if result.PlaceID != "place-1" || result.DisplayName != "Blue Bottle Coffee" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.OpeningHours == nil || result.OpeningHours.OpenNow == nil || !*result.OpeningHours.OpenNow {
		t.Fatalf("unexpected opening hours: %+v", result.OpeningHours)
	}
	if result.Rating == nil || *result.Rating != 4.6 {
		t.Fatalf("unexpected rating: %+v", result.Rating)
	}
	if len(result.AddressComponents) != 1 {
		t.Fatalf("address components = %+v", result.AddressComponents)
	}
}

func TestGetPlace_errorPaths(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		client := &PlacesClient{}
		_, err := client.GetPlace(context.Background(), "place-1")
		if err == nil || !strings.Contains(err.Error(), "GOOGLE_PLACE_API_KEY") {
			t.Fatalf("err = %v, want missing key", err)
		}
	})

	t.Run("http error", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"message":"place not found"}}`)
		})
		_, err := client.GetPlace(context.Background(), "place-1")
		if err == nil || !strings.Contains(err.Error(), "place not found") {
			t.Fatalf("err = %v, want place not found", err)
		}
	})
}
