package google_places

import "testing"

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

func TestInfoLines(t *testing.T) {
	old := GooglePlaceAPIKey
	defer func() { GooglePlaceAPIKey = old }()

	GooglePlaceAPIKey = ""
	lines := InfoLines(t.TempDir())
	if len(lines) != 1 || lines[0] != "   Status:     disabled" {
		t.Fatalf("disabled InfoLines() = %v", lines)
	}

	GooglePlaceAPIKey = "configured"
	lines = InfoLines(t.TempDir())
	if len(lines) != 1 || lines[0] != "   Status:     enabled" {
		t.Fatalf("enabled InfoLines() = %v", lines)
	}
}
