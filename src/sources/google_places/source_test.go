package google_places

import "testing"

// Verifies configuration detection follows whether the build-time Places API key is present.
func TestIsConfigured(t *testing.T) {
	old := GooglePlaceAPIKey
	defer func() { GooglePlaceAPIKey = old }()

	GooglePlaceAPIKey = ""
	if IsConfigured() {
		t.Fatal("expected empty key to be unconfigured")
	}

	GooglePlaceAPIKey = "configured"
	if !IsConfigured() {
		t.Fatal("expected non-empty key to be configured")
	}
}

// Verifies the source methods expose the expected live-only behavior and no local search entries.
func TestSourceMethods(t *testing.T) {
	src := &Source{}

	if src.Name() != "google_places" {
		t.Fatalf("Name() = %q", src.Name())
	}
	if src.Description() != "Google Places" {
		t.Fatalf("Description() = %q", src.Description())
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if err := src.Reset(t.TempDir()); err != nil {
		t.Fatalf("Reset(): %v", err)
	}

	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries(): %v", err)
	}
	if entries != nil {
		t.Fatalf("SearchEntries() = %v, want nil", entries)
	}
}

// Verifies InfoLines returns nil regardless of build-time key presence.
func TestInfoLines(t *testing.T) {
	old := GooglePlaceAPIKey
	defer func() { GooglePlaceAPIKey = old }()

	GooglePlaceAPIKey = ""
	if lines := InfoLines(t.TempDir()); len(lines) != 0 {
		t.Fatalf("expected empty InfoLines, got %v", lines)
	}

	GooglePlaceAPIKey = "configured"
	if lines := InfoLines(t.TempDir()); len(lines) != 0 {
		t.Fatalf("expected empty InfoLines, got %v", lines)
	}
}
