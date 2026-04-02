package brave_search

import "net/http"

// NewSource builds the live-only Brave Search source; `dataDir` is ignored because this source stores no local state.
func NewSource(dataDir string) *Source {
	_ = dataDir
	return &Source{
		client: &BraveClient{
			httpClient: http.DefaultClient,
			baseURL:    defaultBraveBaseURL,
			apiKey:     BraveAPIKey,
		},
	}
}
