package google_places

import "net/http"

// NewSource creates a Google Places source rooted at dataDir.
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
