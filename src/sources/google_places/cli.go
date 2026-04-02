// Package google_places implements live Google Places API lookups for MCP tools (no local index).
package google_places

// InfoLines reports whether the binary was built with a Places API key; there is no per-user data directory state.
func InfoLines(dataDir string) []string {
	_ = dataDir
	if !IsConfigured() {
		return []string{
			"   Status:     disabled",
		}
	}
	return []string{
		"   Status:     enabled",
	}
}
