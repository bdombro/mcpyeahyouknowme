package registry

import (
	"os"
	"path/filepath"
	"testing"

	"mcpyeahyouknowme/sources/google_places"
	"mcpyeahyouknowme/sources/gsuite"
)

// Verifies descriptor lookup returns the expected known sources and rejects unknown names.
func TestFind(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "whatsapp", want: true},
		{name: "gsuite", want: true},
		{name: "google_places", want: true},
		{name: "unknown", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := Find(tt.name)
			if ok != tt.want {
				t.Errorf("Find(%q) ok = %v, want %v", tt.name, ok, tt.want)
			}
		})
	}
}

// Verifies source construction returns working source instances for registered names and nil for unknown ones.
func TestNewSource(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"whatsapp", "gsuite", "google_places"} {
		t.Run(name, func(t *testing.T) {
			src := NewSource(name, dir)
			if src == nil {
				t.Fatalf("NewSource(%q) returned nil", name)
			}
			defer src.Close()
			if src.Name() != name {
				t.Errorf("src.Name() = %q, want %q", src.Name(), name)
			}
		})
	}

	if src := NewSource("unknown", dir); src != nil {
		t.Errorf("NewSource(unknown) = %v, want nil", src)
	}
}

// Verifies registry-level auth checks delegate to each source's local auth/config state.
func TestIsAuthenticated(t *testing.T) {
	dir := t.TempDir()
	if IsAuthenticated("whatsapp", dir) {
		t.Fatal("expected whatsapp auth to be false without a session")
	}
	if IsAuthenticated("gsuite", dir) {
		t.Fatal("expected gsuite auth to be false without a token")
	}
	if err := os.WriteFile(filepath.Join(dir, "gsuite_token.json"), []byte(`{"access_token":"x"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if !IsAuthenticated("gsuite", dir) {
		t.Fatal("expected gsuite auth to be true with a token")
	}
	oldGooglePlacesKey := google_places.GooglePlaceAPIKey
	google_places.GooglePlaceAPIKey = "test-key"
	defer func() {
		google_places.GooglePlaceAPIKey = oldGooglePlacesKey
	}()
	if !IsAuthenticated("google_places", dir) {
		t.Fatal("expected google_places auth to be true with a configured key")
	}
	if !IsAuthenticated("unknown", dir) {
		t.Fatal("unknown sources should default to authenticated")
	}
}

// Verifies loading all sources returns one instance per registered descriptor.
func TestLoadAll(t *testing.T) {
	sources := LoadAll(t.TempDir())
	if len(sources) != len(All) {
		t.Fatalf("LoadAll() len = %d, want %d", len(sources), len(All))
	}
	for _, src := range sources {
		defer src.Close()
	}
}

// Verifies descriptors expose the expected lifecycle and availability capability flags for each built-in source.
func TestDescriptorCapabilities(t *testing.T) {
	tests := []struct {
		name          string
		indexGlobally bool
		runsCore      bool
		hasIsEnabled  bool
		hasReason     bool
	}{
		{name: "whatsapp", indexGlobally: true, runsCore: true},
		{name: "gsuite", indexGlobally: true, runsCore: true, hasIsEnabled: true, hasReason: true},
		{name: "google_places", indexGlobally: false, runsCore: false, hasIsEnabled: true, hasReason: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desc, ok := Find(tt.name)
			if !ok {
				t.Fatalf("missing descriptor %q", tt.name)
			}
			if desc.IndexGlobally != tt.indexGlobally {
				t.Fatalf("IndexGlobally = %v, want %v", desc.IndexGlobally, tt.indexGlobally)
			}
			if desc.RunsCore != tt.runsCore {
				t.Fatalf("RunsCore = %v, want %v", desc.RunsCore, tt.runsCore)
			}
			if (desc.IsEnabled != nil) != tt.hasIsEnabled {
				t.Fatalf("IsEnabled presence = %v, want %v", desc.IsEnabled != nil, tt.hasIsEnabled)
			}
			if (desc.UnavailableReason != "") != tt.hasReason {
				t.Fatalf("UnavailableReason presence = %v, want %v", desc.UnavailableReason != "", tt.hasReason)
			}
		})
	}
}

// Verifies availability checks surface missing build-time credentials for gated sources while leaving WhatsApp always available.
func TestIsAvailable(t *testing.T) {
	oldGooglePlacesKey := google_places.GooglePlaceAPIKey
	oldGoogleClientID := gsuite.GoogleClientID
	oldGoogleClientSecret := gsuite.GoogleClientSecret
	defer func() {
		google_places.GooglePlaceAPIKey = oldGooglePlacesKey
		gsuite.GoogleClientID = oldGoogleClientID
		gsuite.GoogleClientSecret = oldGoogleClientSecret
	}()

	if available, reason := IsAvailable("whatsapp"); !available || reason != "" {
		t.Fatalf("whatsapp availability = (%v, %q), want (true, empty)", available, reason)
	}

	gsuite.GoogleClientID = ""
	gsuite.GoogleClientSecret = ""
	if available, reason := IsAvailable("gsuite"); available || reason == "" {
		t.Fatalf("gsuite availability = (%v, %q), want unavailable with reason", available, reason)
	}

	gsuite.GoogleClientID = "client-id"
	gsuite.GoogleClientSecret = "client-secret"
	if available, reason := IsAvailable("gsuite"); !available || reason != "" {
		t.Fatalf("gsuite availability = (%v, %q), want available", available, reason)
	}

	google_places.GooglePlaceAPIKey = ""
	if available, reason := IsAvailable("google_places"); available || reason == "" {
		t.Fatalf("google_places availability = (%v, %q), want unavailable with reason", available, reason)
	}
}

// Verifies Names preserves the registered source order used by CLI and runtime iteration.
func TestNames(t *testing.T) {
	names := Names()
	if len(names) != len(All) {
		t.Fatalf("Names() len = %d, want %d", len(names), len(All))
	}
	if names[0] != "whatsapp" || names[1] != "gsuite" || names[2] != "google_places" {
		t.Fatalf("unexpected names order: %v", names)
	}
}
