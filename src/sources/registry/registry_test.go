package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFind(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "whatsapp", want: true},
		{name: "gsuite", want: true},
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

func TestNewSource(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"whatsapp", "gsuite"} {
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
	if !IsAuthenticated("unknown", dir) {
		t.Fatal("unknown sources should default to authenticated")
	}
}

func TestLoadAll(t *testing.T) {
	sources := LoadAll(t.TempDir())
	if len(sources) != len(All) {
		t.Fatalf("LoadAll() len = %d, want %d", len(sources), len(All))
	}
	for _, src := range sources {
		defer src.Close()
	}
}
