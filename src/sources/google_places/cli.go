package google_places

// InfoLines reports build-time Places availability for `info`; `dataDir` is ignored because this source has no on-disk state.
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
