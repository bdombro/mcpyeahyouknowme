package google_places

import "net/http"

// NewSource builds the live-only Google Places source; `dataDir` is ignored because this source stores no local state.
func NewSource(dataDir string) *Source {
	_ = dataDir
	return &Source{
		client: &PlacesClient{
			httpClient: http.DefaultClient,
			baseURL:    defaultPlacesBaseURL,
			apiKey:     GooglePlaceAPIKey,
		},
	}
}
