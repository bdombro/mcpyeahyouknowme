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

// Returns the source name so the indexing test double satisfies core.DataSource.
func (s *testMCPSource) Name() string                               { return s.name }
// Returns the display label so the indexing test double satisfies core.DataSource.
func (s *testMCPSource) Description() string                        { return s.name }
// Exposes no tools because this indexing test double only exercises SearchEntries behavior.
func (s *testMCPSource) RegisterTools(*server.MCPServer)            {}
// Returns seeded search entries and increments a call counter so the test can verify indexing eligibility.
func (s *testMCPSource) SearchEntries() ([]core.SearchEntry, error) { s.calls++; return s.entries, nil }
// Satisfies the reset method required by the data-source interface without mutating test state.
func (s *testMCPSource) Reset(string) error                         { return nil }
// Closes nothing because this indexing test double owns no resources.
func (s *testMCPSource) Close() error                               { return nil }

type fakeIndexer struct {
	indexed [][]core.SearchEntry
	updated []string
}

// Records indexed batches so the test can verify which sources were actually passed to the indexer.
func (f *fakeIndexer) IndexEntries(entries []core.SearchEntry) error {
	f.indexed = append(f.indexed, entries)
	return nil
}

// Records timestamp updates so the test can verify only globally indexed sources advance their watermark.
func (f *fakeIndexer) UpdateSourceTimestamp(source string, _ time.Time) {
	f.updated = append(f.updated, source)
}

// Verifies indexing skips sources whose descriptors are marked non-global even when they can return entries.
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
