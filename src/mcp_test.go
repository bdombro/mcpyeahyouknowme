package main

import (
	"testing"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

type testMCPSource struct {
	name    string
	entries []core.SearchEntry
	calls   int
}

func (s *testMCPSource) Name() string                               { return s.name }
func (s *testMCPSource) Description() string                        { return s.name }
func (s *testMCPSource) RegisterTools(*server.MCPServer)            {}
func (s *testMCPSource) SearchEntries() ([]core.SearchEntry, error) { s.calls++; return s.entries, nil }
func (s *testMCPSource) Reset(string) error                         { return nil }
func (s *testMCPSource) Close() error                               { return nil }

type fakeIndexer struct {
	indexed [][]core.SearchEntry
	updated []string
}

func (f *fakeIndexer) IndexEntries(entries []core.SearchEntry) error {
	f.indexed = append(f.indexed, entries)
	return nil
}

func (f *fakeIndexer) UpdateSourceTimestamp(source string, _ time.Time) {
	f.updated = append(f.updated, source)
}

func TestIndexSources_skipsNonIndexedSources(t *testing.T) {
	indexedSrc := &testMCPSource{
		name:    "indexed",
		entries: []core.SearchEntry{{Source: "indexed", SourceID: "1", ContentType: "x", Title: "t", Content: "c"}},
	}
	onDemandSrc := &testMCPSource{
		name:    "ondemand",
		entries: []core.SearchEntry{{Source: "ondemand", SourceID: "2", ContentType: "x", Title: "t", Content: "c"}},
	}
	store := &fakeIndexer{}

	indexSources(store, []activeSource{
		{
			desc: registry.Descriptor{Name: "indexed", IndexGlobally: true},
			src:  indexedSrc,
		},
		{
			desc: registry.Descriptor{Name: "ondemand", IndexGlobally: false},
			src:  onDemandSrc,
		},
	})

	if indexedSrc.calls != 1 {
		t.Fatalf("expected indexed source SearchEntries to be called once, got %d", indexedSrc.calls)
	}
	if onDemandSrc.calls != 0 {
		t.Fatalf("expected on-demand source SearchEntries not to be called, got %d", onDemandSrc.calls)
	}
	if len(store.indexed) != 1 {
		t.Fatalf("expected one indexed batch, got %d", len(store.indexed))
	}
	if len(store.updated) != 1 || store.updated[0] != "indexed" {
		t.Fatalf("expected only indexed source timestamp update, got %v", store.updated)
	}
}
