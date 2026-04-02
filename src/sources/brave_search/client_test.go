package brave_search

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Verifies web search sends the expected request shape and maps a successful Brave response into payloads.
func TestWebSearch_success(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/res/v1/web/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Subscription-Token"); got != "test-api-key" {
			t.Fatalf("token header = %q", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Fatalf("Accept-Encoding = %q, want gzip", got)
		}
		q := r.URL.Query()
		if q.Get("q") != "coffee" || q.Get("count") != "3" || q.Get("offset") != "1" {
			t.Fatalf("query = %v", q)
		}
		if q.Get("country") != "US" || q.Get("search_lang") != "en" || q.Get("ui_lang") != "en-US" {
			t.Fatalf("lang/country = %v", q)
		}
		if q.Get("safesearch") != "strict" || q.Get("freshness") != "pw" {
			t.Fatalf("filters = %v", q)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"query": {"original": "coffee", "more_results_available": true},
			"web": {"results": [
				{"title": "Coffee", "url": "https://example.com/c", "description": "desc", "age": "2024-01-01"}
			]}
		}`)
	})

	payload, err := client.WebSearch(context.Background(), WebSearchOptions{
		Query: "coffee", Count: 3, Offset: 1, Country: "US",
		SearchLang: "en", UILang: "en-US", SafeSearch: "strict", Freshness: "pw",
	})
	if err != nil {
		t.Fatalf("WebSearch(): %v", err)
	}
	if payload.Query != "coffee" || !payload.MoreResultsAvailable || len(payload.Results) != 1 {
		t.Fatalf("payload: %+v", payload)
	}
	if payload.Results[0].Title != "Coffee" || payload.Results[0].URL != "https://example.com/c" {
		t.Fatalf("result: %+v", payload.Results[0])
	}
}

// Verifies web search surfaces missing-key, upstream HTTP, and decode failures as actionable errors.
func TestWebSearch_errorPaths(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		client := &BraveClient{}
		_, err := client.WebSearch(context.Background(), WebSearchOptions{Query: "x"})
		if err == nil || !strings.Contains(err.Error(), "BRAVE_API_KEY") {
			t.Fatalf("err = %v, want missing key", err)
		}
	})

	t.Run("http error", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"message":"denied"}}`)
		})
		_, err := client.WebSearch(context.Background(), WebSearchOptions{Query: "x"})
		if err == nil || !strings.Contains(err.Error(), "denied") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, `{"web":`)
		})
		_, err := client.WebSearch(context.Background(), WebSearchOptions{Query: "x"})
		if err == nil || !strings.Contains(err.Error(), "decode brave search response") {
			t.Fatalf("err = %v", err)
		}
	})
}

// Verifies web search rejects empty queries before any upstream HTTP request.
func TestWebSearch_emptyQuery(t *testing.T) {
	client := newTestClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("unexpected upstream call")
	})
	_, err := client.WebSearch(context.Background(), WebSearchOptions{Query: "  "})
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("err = %v", err)
	}
}

// Verifies count and offset are clamped to Brave-supported bounds.
func TestWebSearch_countOffsetClamping(t *testing.T) {
	var got url.Values
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		io.WriteString(w, `{"query":{},"web":{"results":[]}}`)
	})
	client.WebSearch(context.Background(), WebSearchOptions{Query: "q", Count: 0, Offset: 0})
	if got.Get("count") != "20" || got.Get("offset") != "0" {
		t.Fatalf("defaults: count=%q offset=%q", got.Get("count"), got.Get("offset"))
	}
	client.WebSearch(context.Background(), WebSearchOptions{Query: "q", Count: 99, Offset: 20})
	if got.Get("count") != "20" || got.Get("offset") != "9" {
		t.Fatalf("clamped: count=%q offset=%q", got.Get("count"), got.Get("offset"))
	}
	client.WebSearch(context.Background(), WebSearchOptions{Query: "q", Count: 5, Offset: -3})
	if got.Get("offset") != "0" {
		t.Fatalf("negative offset: want 0, got %q", got.Get("offset"))
	}
}

// Verifies API-error formatting falls back to status text or raw body when structured error JSON is absent.
func TestApiError_emptyBody(t *testing.T) {
	err := apiError(http.StatusBadGateway, []byte(""))
	if !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("expected status text fallback, got %v", err)
	}
	err = apiError(http.StatusBadGateway, []byte("raw error text"))
	if !strings.Contains(err.Error(), "raw error text") {
		t.Fatalf("expected raw body in error, got %v", err)
	}
}

// Verifies LookupURL returns exact_match when a result URL canonically equals the requested URL.
func TestLookupURL_exactMatch(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "https://example.com/foo/" {
			t.Fatalf("q = %q", r.URL.Query().Get("q"))
		}
		io.WriteString(w, `{
			"query": {"original": "https://example.com/foo/"},
			"web": {"results": [
				{"title": "Other", "url": "https://other.com/", "description": ""},
				{"title": "Foo", "url": "https://example.com/foo", "description": "d"}
			]}
		}`)
	})
	out, err := client.LookupURL(context.Background(), "https://example.com/foo/")
	if err != nil {
		t.Fatalf("LookupURL(): %v", err)
	}
	if !out.ExactMatch || out.Result.Title != "Foo" {
		t.Fatalf("unexpected: %+v", out)
	}
}

// Verifies LookupURL falls back to the first non-empty result URL when no canonical match exists.
func TestLookupURL_fallback(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"query": {},
			"web": {"results": [
				{"title": "Hit", "url": "https://nomatch.com/", "description": "x"}
			]}
		}`)
	})
	out, err := client.LookupURL(context.Background(), "https://target.example/page")
	if err != nil {
		t.Fatalf("LookupURL(): %v", err)
	}
	if out.ExactMatch || out.Result.Title != "Hit" {
		t.Fatalf("unexpected: %+v", out)
	}
}

// Verifies LookupURL errors when every hit omits a usable URL.
func TestLookupURL_onlyEmptyURLs(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"query": {},
			"web": {"results": [
				{"title": "A", "url": ""},
				{"title": "B", "description": "x"}
			]}
		}`)
	})
	_, err := client.LookupURL(context.Background(), "https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "no web search results") {
		t.Fatalf("err = %v", err)
	}
}

// Verifies LookupURL skips candidate URLs that fail canonical parsing before matching a later hit.
func TestLookupURL_skipsUnparsableCandidates(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"query": {},
			"web": {"results": [
				{"title": "Bad", "url": "https://"},
				{"title": "Good", "url": "https://example.com/target", "description": "d"}
			]}
		}`)
	})
	out, err := client.LookupURL(context.Background(), "https://example.com/target")
	if err != nil {
		t.Fatalf("LookupURL(): %v", err)
	}
	if !out.ExactMatch || out.Result.Title != "Good" {
		t.Fatalf("unexpected: %+v", out)
	}
}

// Verifies LookupURL errors when Brave returns no web results.
func TestLookupURL_noResults(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"query":{},"web":{"results":[]}}`)
	})
	_, err := client.LookupURL(context.Background(), "https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "no web search results") {
		t.Fatalf("err = %v", err)
	}
}

// Verifies LookupURL rejects empty and unparseable URLs.
func TestLookupURL_badURL(t *testing.T) {
	client := newTestClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("unexpected upstream")
	})
	_, err := client.LookupURL(context.Background(), "  ")
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("err = %v", err)
	}
	_, err = client.LookupURL(context.Background(), "://nohost")
	if err == nil {
		t.Fatal("expected error for bad url")
	}
}

// Verifies canonicalURLKey normalizes hosts, default ports, and trailing slashes consistently.
func TestCanonicalURLKey_equivalence(t *testing.T) {
	a, err := canonicalURLKey("https://Example.COM/foo/")
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalURLKey("http://example.com:80/foo")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("expected http:80 and https to differ, got %q", a)
	}
	c, err := canonicalURLKey("https://example.com:443/foo/")
	if err != nil {
		t.Fatal(err)
	}
	if a != c {
		t.Fatalf("want %q == %q", a, c)
	}
}

// Verifies canonicalURLKey covers parse failures, missing hosts, non-default ports, paths, queries, and scheme coercion.
func TestCanonicalURLKey_branches(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, err := canonicalURLKey("  "); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("parse error", func(t *testing.T) {
		if _, err := canonicalURLKey("http://a b"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("missing host", func(t *testing.T) {
		if _, err := canonicalURLKey("https://"); err == nil || !strings.Contains(err.Error(), "host") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ftp coerced", func(t *testing.T) {
		got, err := canonicalURLKey("ftp://Example.COM/path/")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, "https://example.com/path") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("non-default port", func(t *testing.T) {
		got, err := canonicalURLKey("http://example.com:8080/foo")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, ":8080") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("with query", func(t *testing.T) {
		got, err := canonicalURLKey("https://example.com/a?z=1&y=2")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "?") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("query param order equivalence", func(t *testing.T) {
		a, err := canonicalURLKey("https://example.com/p?b=2&a=1")
		if err != nil {
			t.Fatal(err)
		}
		b, err := canonicalURLKey("https://example.com/p?a=1&b=2")
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("want equal keys, got %q vs %q", a, b)
		}
	})
	t.Run("no scheme prefix", func(t *testing.T) {
		got, err := canonicalURLKey("example.com/foo")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "example.com/foo") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("http default port branch", func(t *testing.T) {
		got, err := canonicalURLKey("http://example.com/foo")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, "http://example.com/foo") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("empty path becomes slash", func(t *testing.T) {
		got, err := canonicalURLKey("https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://example.com/" {
			t.Fatalf("got %q", got)
		}
	})
}

// Verifies LookupURL continues past empty URL entries when scanning for a canonical match.
func TestLookupURL_emptyCandidateContinue(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{
			"query": {},
			"web": {"results": [
				{"title": "Skip", "url": ""},
				{"title": "Hit", "url": "https://example.com/x", "description": "d"}
			]}
		}`)
	})
	out, err := client.LookupURL(context.Background(), "https://example.com/x")
	if err != nil {
		t.Fatalf("LookupURL(): %v", err)
	}
	if !out.ExactMatch {
		t.Fatalf("want exact match: %+v", out)
	}
}
