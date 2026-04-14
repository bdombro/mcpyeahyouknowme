// Package google_places implements live Google Places API lookups for MCP tools (no local index).
package google_places

// InfoLines returns nothing; Places availability is shown via the unavailable path when no API key is built in.
func InfoLines(_ string) []string {
	return nil
}
