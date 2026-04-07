// Package brave_search implements live Brave Search API lookups for MCP tools (no local index).
package brave_search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const defaultBraveBaseURL = "https://api.search.brave.com/res/v1"

var errBraveAPIKeyMissing = errors.New("BRAVE_API_KEY is not configured in this build")

// BraveAPIKey is injected at build time via ldflags from BRAVE_API_KEY in .env.
var BraveAPIKey string

// WebSearchOptions carries optional query parameters for Brave web search.
type WebSearchOptions struct {
	Query      string
	Count      int
	Offset     int
	Country    string
	SearchLang string
	UILang     string
	SafeSearch string
	Freshness  string
}

// WebResult is one normalized web hit from Brave Search.
type WebResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Age         string `json:"age,omitempty"`
}

// WebSearchPayload is the stable JSON shape returned by brave_search_web.
type WebSearchPayload struct {
	Query                 string      `json:"query"`
	MoreResultsAvailable  bool        `json:"more_results_available"`
	Results               []WebResult `json:"results"`
}

// URLLookupPayload is the stable JSON shape returned by brave_search_url.
type URLLookupPayload struct {
	ExactMatch bool      `json:"exact_match"`
	Result     WebResult `json:"result"`
}

// BraveClient calls the Brave Search web API used by brave_search_web and brave_search_url.
type BraveClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

type webSearchWire struct {
	Query struct {
		Original             string `json:"original"`
		MoreResultsAvailable *bool  `json:"more_results_available"`
	} `json:"query"`
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Age         string `json:"age"`
		} `json:"results"`
	} `json:"web"`
}

type apiErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// WebSearch issues a live GET to Brave web search and returns normalized results.
func (c *BraveClient) WebSearch(ctx context.Context, opts WebSearchOptions) (*WebSearchPayload, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, errors.New("query is required")
	}
	if err := c.validate(); err != nil {
		return nil, err
	}

	count := opts.Count
	if count <= 0 {
		count = 20
	}
	if count > 20 {
		count = 20
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > 9 {
		offset = 9
	}

	q := url.Values{}
	q.Set("q", opts.Query)
	q.Set("count", fmt.Sprintf("%d", count))
	q.Set("offset", fmt.Sprintf("%d", offset))
	addIfNonEmpty := func(key, v string) {
		if strings.TrimSpace(v) != "" {
			q.Set(key, strings.TrimSpace(v))
		}
	}
	addIfNonEmpty("country", opts.Country)
	addIfNonEmpty("search_lang", opts.SearchLang)
	addIfNonEmpty("ui_lang", opts.UILang)
	addIfNonEmpty("safesearch", opts.SafeSearch)
	addIfNonEmpty("freshness", opts.Freshness)

	reqURL := c.baseURL + "/web/search?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// nocov
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// Do not set Accept-Encoding: gzip here. net/http only transparently decompresses
	// gzip responses when the Transport adds Accept-Encoding itself; a manual header
	// leaves the body compressed and breaks json.Unmarshal (first byte 0x1f).
	req.Header.Set("X-Subscription-Token", c.apiKey)

	var wire webSearchWire
	if err := c.do(req, &wire); err != nil {
		return nil, err
	}

	out := &WebSearchPayload{
		Query:                wire.Query.Original,
		MoreResultsAvailable: wire.Query.MoreResultsAvailable != nil && *wire.Query.MoreResultsAvailable,
		Results:              make([]WebResult, 0, len(wire.Web.Results)),
	}
	for _, r := range wire.Web.Results {
		out.Results = append(out.Results, WebResult{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
			Age:         r.Age,
		})
	}
	return out, nil
}

// LookupURL runs a web search for the given page URL and picks an exact canonical match when possible, otherwise the first result.
func (c *BraveClient) LookupURL(ctx context.Context, pageURL string) (*URLLookupPayload, error) {
	pageURL = strings.TrimSpace(pageURL)
	if pageURL == "" {
		return nil, errors.New("url is required")
	}
	targetKey, err := canonicalURLKey(pageURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	payload, err := c.WebSearch(ctx, WebSearchOptions{Query: pageURL, Count: 20, Offset: 0})
	if err != nil {
		return nil, err
	}
	if len(payload.Results) == 0 {
		return nil, errors.New("no web search results for URL")
	}

	var first WebResult
	for i := range payload.Results {
		cand := payload.Results[i].URL
		if cand == "" {
			continue
		}
		if first.URL == "" {
			first = payload.Results[i]
		}
		key, err := canonicalURLKey(cand)
		if err != nil {
			continue
		}
		if key == targetKey {
			return &URLLookupPayload{ExactMatch: true, Result: payload.Results[i]}, nil
		}
	}
	if first.URL == "" {
		return nil, errors.New("no web search results for URL")
	}

	return &URLLookupPayload{ExactMatch: false, Result: first}, nil
}

// validate rejects client calls when the build or client is missing an API key.
func (c *BraveClient) validate() error {
	if c == nil || c.apiKey == "" {
		return errBraveAPIKeyMissing
	}
	return nil
}

// do executes a Brave HTTP request, decodes success bodies, and rewrites non-2xx responses into API errors.
func (c *BraveClient) do(req *http.Request, dest interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// nocov
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// nocov
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode brave search response: %w", err)
	}
	return nil
}

// apiError formats a non-2xx Brave response body into one actionable Go error.
func apiError(statusCode int, body []byte) error {
	var envelope apiErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return fmt.Errorf("brave search API error (%d): %s", statusCode, envelope.Error.Message)
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = http.StatusText(statusCode)
	}
	return fmt.Errorf("brave search API error (%d): %s", statusCode, text)
}

// canonicalURLKey builds a comparable key for two HTTP(S) URLs (scheme/host/path/query normalized).
func canonicalURLKey(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty url")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", errors.New("missing host")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	var hostPart string
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		hostPart = host
	} else {
		hostPart = net.JoinHostPort(host, port)
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	for len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	sortedQuery := u.Query().Encode()
	if sortedQuery != "" {
		return scheme + "://" + hostPart + path + "?" + sortedQuery, nil
	}
	return scheme + "://" + hostPart + path, nil
}
