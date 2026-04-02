package main

import (
	"context"
	"os"
	"testing"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

type runtimeTestSource struct {
	resetCalled bool
}

// Returns the stub source name so runtime tests can register a minimal data source.
func (r *runtimeTestSource) Name() string                               { return "stub" }
// Returns the stub description so runtime tests satisfy the data-source interface.
func (r *runtimeTestSource) Description() string                        { return "Stub" }
// Registers no tools because runtime tests only exercise daemon lifecycle helpers.
func (r *runtimeTestSource) RegisterTools(*server.MCPServer)            {}
// Returns no entries because runtime tests are focused on reset/start behavior, not indexing.
func (r *runtimeTestSource) SearchEntries() ([]core.SearchEntry, error) { return nil, nil }
// Marks resetCalled so runtime tests can verify the reset path invokes the source.
func (r *runtimeTestSource) Reset(string) error                         { r.resetCalled = true; return nil }
// Closes nothing because the runtime test stub owns no resources.
func (r *runtimeTestSource) Close() error                               { return nil }

// Verifies handleReset keeps the source config entry but clears enabled/reset state instead of deleting it.
func TestHandleReset_disablesSourceInsteadOfDeleting(t *testing.T) {
	core.RegisterKnownSource("stub")
	dir := t.TempDir()
	if err := core.UpdateSourceConfig(dir, "stub", func(sc *core.SourceConfig) {
		sc.Enabled = true
		sc.Reset = true
	}); err != nil {
		t.Fatalf("UpdateSourceConfig: %v", err)
	}

	src := &runtimeTestSource{}
	original := registry.All
	registry.All = []registry.Descriptor{{
		Name:          "stub",
		New:           func(string) core.DataSource { return src },
		IndexGlobally: false,
		RunsCore:      false,
	}}
	t.Cleanup(func() { registry.All = original })

	cfg := core.LoadConfig(dir)
	handleReset(dir, "stub", &cfg)

	if !src.resetCalled {
		t.Fatal("expected reset to be called")
	}
	loaded := core.LoadConfig(dir)
	sc, ok := loaded.Sources["stub"]
	if !ok {
		t.Fatal("expected source entry to remain after reset")
	}
	if sc.Enabled {
		t.Fatal("expected source to be disabled after reset")
	}
	if sc.Reset {
		t.Fatal("expected reset flag to be cleared")
	}
}

// Verifies startSource ignores enabled sources that declare no background core service.
func TestStartSource_skipsEnabledNonCoreSources(t *testing.T) {
	src := &runtimeTestSource{}
	original := registry.All
	registry.All = []registry.Descriptor{{
		Name:          "stub",
		New:           func(string) core.DataSource { return src },
		IndexGlobally: false,
		RunsCore:      false,
	}}
	t.Cleanup(func() { registry.All = original })

	running := map[string]context.CancelFunc{}
	startSource(t.TempDir(), "stub", running)
	if len(running) != 0 {
		t.Fatalf("expected no running sources, got %d", len(running))
	}
}

// Verifies startSource skips unavailable sources and reports the reason instead of starting them.
func TestStartSource_skipsUnavailableSources(t *testing.T) {
	src := &runtimeTestSource{}
	original := registry.All
	registry.All = []registry.Descriptor{{
		Name:              "stub",
		New:               func(string) core.DataSource { return src },
		IsEnabled:         func() bool { return false },
		UnavailableReason: "missing build-time credential",
		IndexGlobally:     false,
		RunsCore:          true,
	}}
	t.Cleanup(func() { registry.All = original })

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
	}()

	running := map[string]context.CancelFunc{}
	startSource(t.TempDir(), "stub", running)

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out := make([]byte, 256)
	n, _ := r.Read(out)
	if got := string(out[:n]); got == "" {
		t.Fatal("expected availability warning on stderr")
	}
	if len(running) != 0 {
		t.Fatalf("expected no running sources, got %d", len(running))
	}
}

// Verifies auth changes trigger restarts while enable/disable transitions are handled elsewhere in the poll loop.
func TestShouldRestartSource(t *testing.T) {
	tests := []struct {
		name string
		prev core.SourceConfig
		next core.SourceConfig
		want bool
	}{
		{
			name: "auth change restarts source",
			prev: core.SourceConfig{Enabled: true, Auth: []byte(`{"apps":{"tasks":true}}`)},
			next: core.SourceConfig{Enabled: true, Auth: []byte(`{"apps":{"tasks":true,"docs":true}}`)},
			want: true,
		},
		{
			name: "same auth does not restart",
			prev: core.SourceConfig{Enabled: true, Auth: []byte(`{"apps":{"tasks":true}}`)},
			next: core.SourceConfig{Enabled: true, Auth: []byte(`{"apps":{"tasks":true}}`)},
			want: false,
		},
		{
			name: "enabled state change handled elsewhere",
			prev: core.SourceConfig{Enabled: false, Auth: []byte(`{"apps":{"tasks":true}}`)},
			next: core.SourceConfig{Enabled: true, Auth: []byte(`{"apps":{"tasks":true,"docs":true}}`)},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRestartSource(tc.prev, tc.next); got != tc.want {
				t.Fatalf("shouldRestartSource() = %v, want %v", got, tc.want)
			}
		})
	}
}
