package brave_search

// InfoLines reports whether the binary was built with a Brave API key; there is no per-user data directory state.
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
