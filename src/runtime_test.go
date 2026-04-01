package main

import (
	"context"
	"testing"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

type runtimeTestSource struct {
	resetCalled bool
}

func (r *runtimeTestSource) Name() string                               { return "stub" }
func (r *runtimeTestSource) Description() string                        { return "Stub" }
func (r *runtimeTestSource) RegisterTools(*server.MCPServer)            {}
func (r *runtimeTestSource) SearchEntries() ([]core.SearchEntry, error) { return nil, nil }
func (r *runtimeTestSource) Reset(string) error                         { r.resetCalled = true; return nil }
func (r *runtimeTestSource) Close() error                               { return nil }

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
