package google_places

// InfoLines reports the google_places build-time configuration status.
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
