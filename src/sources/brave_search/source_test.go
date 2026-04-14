package brave_search

import "testing"

// Verifies configuration detection follows whether the build-time Brave API key is present.
func TestIsConfigured(t *testing.T) {
	old := BraveAPIKey
	defer func() { BraveAPIKey = old }()

	BraveAPIKey = ""
	if IsConfigured() {
		t.Fatal("expected empty key to be unconfigured")
	}

	BraveAPIKey = "configured"
	if !IsConfigured() {
		t.Fatal("expected non-empty key to be configured")
	}
}

// Verifies the source methods expose the expected live-only behavior and no local search entries.
func TestSourceMethods(t *testing.T) {
	src := &Source{}

	if src.Name() != "brave_search" {
		t.Fatalf("Name() = %q", src.Name())
	}
	if src.Description() != "Brave Search" {
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
	old := BraveAPIKey
	defer func() { BraveAPIKey = old }()

	BraveAPIKey = ""
	if lines := InfoLines(t.TempDir()); len(lines) != 0 {
		t.Fatalf("expected empty InfoLines, got %v", lines)
	}

	BraveAPIKey = "configured"
	if lines := InfoLines(t.TempDir()); len(lines) != 0 {
		t.Fatalf("expected empty InfoLines, got %v", lines)
	}
}
