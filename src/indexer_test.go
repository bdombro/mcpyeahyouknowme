package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

type indexerTestSource struct {
	name    string
	entries []core.SearchEntry
}

type spySourceIndexer struct {
	indexed        [][]core.SearchEntry
	prunedSource   []string
	prunedEntries  [][]core.SearchEntry
	pruneErr       error
	updatedSources []string
	updatedTimes   []time.Time
}

// Returns the configured source name so indexer tests can verify per-source callbacks precisely.
func (s *indexerTestSource) Name() string {
	return s.name
}

// Returns a stable description because indexer tests only care about SearchEntries output.
func (s *indexerTestSource) Description() string {
	return s.name
}

// Registers nothing because indexer tests never exercise MCP tool exposure.
func (s *indexerTestSource) RegisterTools(_ *server.MCPServer) {}

// Returns the configured entries so indexer tests can drive indexing and pruning deterministically.
func (s *indexerTestSource) SearchEntries() ([]core.SearchEntry, error) {
	return s.entries, nil
}

// Resets nothing because indexer tests only exercise global indexing behavior.
func (s *indexerTestSource) Reset(string) error {
	return nil
}

// Closes nothing because the test source owns no external resources.
func (s *indexerTestSource) Close() error {
	return nil
}

// Records indexed entries so tests can verify indexSources forwards the latest SearchEntries output unchanged.
func (s *spySourceIndexer) IndexEntries(entries []core.SearchEntry) error {
	copied := append([]core.SearchEntry(nil), entries...)
	s.indexed = append(s.indexed, copied)
	return nil
}

// Records prune requests so tests can verify indexSources removes stale rows after each upsert.
func (s *spySourceIndexer) PruneSource(source string, current []core.SearchEntry) error {
	s.prunedSource = append(s.prunedSource, source)
	copied := append([]core.SearchEntry(nil), current...)
	s.prunedEntries = append(s.prunedEntries, copied)
	return s.pruneErr
}

// Records source timestamps so tests can assert successful prune completion gates the last-indexed marker.
func (s *spySourceIndexer) UpdateSourceTimestamp(source string, ts time.Time) {
	s.updatedSources = append(s.updatedSources, source)
	s.updatedTimes = append(s.updatedTimes, ts)
}

// Verifies indexSources prunes each indexed source before updating its timestamp.
func TestIndexSources_prunesIndexedSources(t *testing.T) {
	entries := []core.SearchEntry{
		{Source: "notebook", SourceID: "note#title", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"},
		{Source: "notebook", SourceID: "note#chunk0", ContentType: "note_content", Title: "John Thomas", Content: "John Thomas has 2 kids"},
	}
	store := &spySourceIndexer{}
	sources := []activeSource{{
		desc: registry.Descriptor{Name: "notebook", IndexGlobally: true},
		src:  &indexerTestSource{name: "notebook", entries: entries},
	}}

	if completed := indexSources(context.Background(), store, sources); !completed {
		t.Fatal("expected indexing to complete")
	}
	if len(store.indexed) != 1 {
		t.Fatalf("expected one index call, got %d", len(store.indexed))
	}
	if len(store.prunedSource) != 1 || store.prunedSource[0] != "notebook" {
		t.Fatalf("expected prune for notebook, got %#v", store.prunedSource)
	}
	if len(store.prunedEntries) != 1 || len(store.prunedEntries[0]) != len(entries) {
		t.Fatalf("expected prune to receive %d entries, got %#v", len(entries), store.prunedEntries)
	}
	if len(store.updatedSources) != 1 || store.updatedSources[0] != "notebook" {
		t.Fatalf("expected timestamp update for notebook, got %#v", store.updatedSources)
	}
}

// Verifies indexSources skips timestamp updates when prune fails so stale source state is retried later.
func TestIndexSources_pruneErrorSkipsTimestampUpdate(t *testing.T) {
	store := &spySourceIndexer{pruneErr: errors.New("boom")}
	sources := []activeSource{{
		desc: registry.Descriptor{Name: "notebook", IndexGlobally: true},
		src:  &indexerTestSource{name: "notebook", entries: []core.SearchEntry{{Source: "notebook", SourceID: "note#title", ContentType: "note_title", Title: "John Thomas", Content: "John Thomas"}}},
	}}

	if completed := indexSources(context.Background(), store, sources); !completed {
		t.Fatal("expected indexing to complete despite prune failure")
	}
	if len(store.prunedSource) != 1 {
		t.Fatalf("expected prune to be attempted once, got %d", len(store.prunedSource))
	}
	if len(store.updatedSources) != 0 {
		t.Fatalf("expected no timestamp update after prune failure, got %#v", store.updatedSources)
	}
}
