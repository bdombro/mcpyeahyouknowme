package main

import (
	"bytes"
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

type reuseTestSource struct {
	name       string
	closeCount *int
}

type incrementalIndexerTestSource struct {
	indexerTestSource
	changed bool
}

type streamingIndexerTestSource struct {
	name    string
	batches [][]core.SearchEntry
	err     error
}

type spySourceIndexer struct {
	indexed        [][]core.SearchEntry
	prunedSource   []string
	prunedEntries  [][]indexKey
	pruneErr       error
	updatedSources []string
	updatedTimes   []time.Time
	lastIndexed    map[string]time.Time
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

// Returns the seeded source name so reconcile tests can track one reusable source.
func (s *reuseTestSource) Name() string { return s.name }

// Returns the seeded description because reconcile tests only care about lifecycle.
func (s *reuseTestSource) Description() string { return s.name }

// Registers nothing because reconcile tests never exercise MCP tool exposure.
func (s *reuseTestSource) RegisterTools(_ *server.MCPServer) {}

// Returns no entries because reconcile tests only care about source reuse decisions.
func (s *reuseTestSource) SearchEntries() ([]core.SearchEntry, error) { return nil, nil }

// Resets nothing because reconcile tests only cover source construction and teardown.
func (s *reuseTestSource) Reset(string) error { return nil }

// Records closes so reconcile tests can assert stale sources are released promptly.
func (s *reuseTestSource) Close() error {
	if s.closeCount != nil {
		*s.closeCount = *s.closeCount + 1
	}
	return nil
}

// Reports the seeded incremental-change result so indexer unit tests can cover skip decisions directly.
func (s *incrementalIndexerTestSource) HasChangesSince(time.Time) bool { return s.changed }

// Returns the seeded source name so streaming indexer tests satisfy the data-source interface.
func (s *streamingIndexerTestSource) Name() string { return s.name }

// Returns the seeded description because streaming indexer tests only care about emit behavior.
func (s *streamingIndexerTestSource) Description() string { return s.name }

// Registers nothing because streaming indexer tests never exercise MCP tool exposure.
func (s *streamingIndexerTestSource) RegisterTools(_ *server.MCPServer) {}

// Returns no fallback entries because streaming indexer tests target the streaming path explicitly.
func (s *streamingIndexerTestSource) SearchEntries() ([]core.SearchEntry, error) { return nil, nil }

// Resets nothing because streaming indexer tests only cover indexing behavior.
func (s *streamingIndexerTestSource) Reset(string) error { return nil }

// Closes nothing because the streaming indexer test source owns no resources.
func (s *streamingIndexerTestSource) Close() error { return nil }

// Emits seeded batches and then returns the configured error so indexer tests can cover stream error handling.
func (s *streamingIndexerTestSource) StreamSearchEntries(emit func([]core.SearchEntry) error) error {
	for _, batch := range s.batches {
		if err := emit(batch); err != nil {
			return err
		}
	}
	return s.err
}

// Records indexed entries so tests can verify indexSources forwards the latest SearchEntries output unchanged.
func (s *spySourceIndexer) IndexEntries(entries []core.SearchEntry) error {
	copied := append([]core.SearchEntry(nil), entries...)
	s.indexed = append(s.indexed, copied)
	return nil
}

// Records prune requests so tests can verify indexSources removes stale rows after each upsert.
func (s *spySourceIndexer) PruneSourceKeys(source string, current []indexKey) error {
	s.prunedSource = append(s.prunedSource, source)
	copied := append([]indexKey(nil), current...)
	s.prunedEntries = append(s.prunedEntries, copied)
	return s.pruneErr
}

// Records source timestamps so tests can assert successful prune completion gates the last-indexed marker.
func (s *spySourceIndexer) UpdateSourceTimestamp(source string, ts time.Time) {
	s.updatedSources = append(s.updatedSources, source)
	s.updatedTimes = append(s.updatedTimes, ts)
}

// Returns a seeded watermark so indexer tests can drive incremental skip behavior.
func (s *spySourceIndexer) LastIndexed(source string) time.Time {
	if s.lastIndexed == nil {
		return time.Time{}
	}
	return s.lastIndexed[source]
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

	if completed := indexSources(context.Background(), store, sources, true); !completed {
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

	if completed := indexSources(context.Background(), store, sources, true); !completed {
		t.Fatal("expected indexing to complete despite prune failure")
	}
	if len(store.prunedSource) != 1 {
		t.Fatalf("expected prune to be attempted once, got %d", len(store.prunedSource))
	}
	if len(store.updatedSources) != 0 {
		t.Fatalf("expected no timestamp update after prune failure, got %#v", store.updatedSources)
	}
}

// Verifies reconcileIndexSources reuses unchanged sources and closes replaced ones when auth/config changes.
func TestReconcileIndexSources_reusesAndReplacesSources(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "reuse", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}

	created := 0
	closed := 0
	original := registry.All
	registry.All = []registry.Descriptor{{
		Name:            "reuse",
		IndexGlobally:   true,
		IsAuthenticated: func(string) bool { return true },
		New: func(string) core.DataSource {
			created++
			return &reuseTestSource{name: "reuse", closeCount: &closed}
		},
	}}
	t.Cleanup(func() { registry.All = original })

	first := reconcileIndexSources(dir, nil)
	if len(first) != 1 {
		t.Fatalf("expected one active source, got %d", len(first))
	}
	firstSrc := first[0].src
	if created != 1 || closed != 0 {
		t.Fatalf("expected one creation and zero closes, got created=%d closed=%d", created, closed)
	}

	second := reconcileIndexSources(dir, first)
	if len(second) != 1 {
		t.Fatalf("expected one reused source, got %d", len(second))
	}
	if second[0].src != firstSrc {
		t.Fatal("expected unchanged config to reuse the existing source instance")
	}
	if created != 1 || closed != 0 {
		t.Fatalf("expected no extra lifecycle work on reuse, got created=%d closed=%d", created, closed)
	}

	if err := core.UpdateSourceConfig(dir, "reuse", func(sc *core.SourceConfig) {
		sc.Auth = []byte(`{"apps":{"docs":true}}`)
	}); err != nil {
		t.Fatalf("UpdateSourceConfig: %v", err)
	}

	third := reconcileIndexSources(dir, second)
	if len(third) != 1 {
		t.Fatalf("expected one replacement source, got %d", len(third))
	}
	if third[0].src == firstSrc {
		t.Fatal("expected auth change to rebuild the source instance")
	}
	if created != 2 || closed != 1 {
		t.Fatalf("expected one replacement lifecycle, got created=%d closed=%d", created, closed)
	}

	if err := core.SetSourceEnabled(dir, "reuse", false); err != nil {
		t.Fatalf("SetSourceEnabled(false): %v", err)
	}
	final := reconcileIndexSources(dir, third)
	if len(final) != 0 {
		t.Fatalf("expected disabled source to be removed, got %d", len(final))
	}
	if closed != 2 {
		t.Fatalf("expected disabled source to be closed, got %d closes", closed)
	}
}

// Verifies reconcileIndexSources closes and skips existing sources when the registry says the source is unavailable.
func TestReconcileIndexSources_closesUnavailableSource(t *testing.T) {
	dir := t.TempDir()

	closed := 0
	original := registry.All
	registry.All = []registry.Descriptor{{
		Name:          "unavail",
		IndexGlobally: true,
		IsEnabled:     func() bool { return false },
		New: func(string) core.DataSource {
			return &reuseTestSource{name: "unavail", closeCount: &closed}
		},
	}}
	t.Cleanup(func() { registry.All = original })

	existing := []activeSource{{
		desc:   registry.Descriptor{Name: "unavail", IndexGlobally: true},
		src:    &reuseTestSource{name: "unavail", closeCount: &closed},
		config: core.SourceConfig{Enabled: true},
	}}

	result := reconcileIndexSources(dir, existing)
	if len(result) != 0 {
		t.Fatalf("expected unavailable source to be excluded, got %d", len(result))
	}
	if closed != 1 {
		t.Fatalf("expected existing unavailable source to be closed, got %d", closed)
	}
}

// Verifies reconcileIndexSources closes and skips sources that are no longer authenticated.
func TestReconcileIndexSources_closesUnauthenticatedSource(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "unauth", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}

	closed := 0
	original := registry.All
	registry.All = []registry.Descriptor{{
		Name:            "unauth",
		IndexGlobally:   true,
		IsAuthenticated: func(string) bool { return false },
		New: func(string) core.DataSource {
			return &reuseTestSource{name: "unauth", closeCount: &closed}
		},
	}}
	t.Cleanup(func() { registry.All = original })

	existing := []activeSource{{
		desc:   registry.Descriptor{Name: "unauth", IndexGlobally: true},
		src:    &reuseTestSource{name: "unauth", closeCount: &closed},
		config: core.SourceConfig{Enabled: true},
	}}

	result := reconcileIndexSources(dir, existing)
	if len(result) != 0 {
		t.Fatalf("expected unauthenticated source to be excluded, got %d", len(result))
	}
	if closed != 1 {
		t.Fatalf("expected unauthenticated source to be closed, got %d", closed)
	}
}

// Verifies reconcileIndexSources skips sources when New returns nil and closes any existing instance.
func TestReconcileIndexSources_skipsNilNewSource(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "nilsrc", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}

	closed := 0
	original := registry.All
	registry.All = []registry.Descriptor{{
		Name:            "nilsrc",
		IndexGlobally:   true,
		IsAuthenticated: func(string) bool { return true },
		New:             func(string) core.DataSource { return nil },
	}}
	t.Cleanup(func() { registry.All = original })

	existing := []activeSource{{
		desc:   registry.Descriptor{Name: "nilsrc", IndexGlobally: true},
		src:    &reuseTestSource{name: "nilsrc", closeCount: &closed},
		config: core.SourceConfig{},
	}}

	result := reconcileIndexSources(dir, existing)
	if len(result) != 0 {
		t.Fatalf("expected nil-New source to produce no active sources, got %d", len(result))
	}
	if closed != 1 {
		t.Fatalf("expected old nil-New source instance to be closed, got %d", closed)
	}
}

// Verifies cloneSourceConfig deep-copies auth bytes so later mutations do not affect reuse comparisons.
func TestCloneSourceConfig_copiesAuth(t *testing.T) {
	original := core.SourceConfig{Enabled: true, Auth: []byte(`{"apps":{"docs":true}}`)}
	cloned := cloneSourceConfig(original)
	original.Auth[0] = 'x'
	if bytes.Equal(original.Auth, cloned.Auth) {
		t.Fatal("expected cloned auth bytes to be independent of the source config")
	}
}

// Verifies sourceConfigEqual checks enabled/reset flags and auth bytes rather than pointer identity.
func TestSourceConfigEqual(t *testing.T) {
	a := core.SourceConfig{Enabled: true, Reset: false, Auth: []byte(`{"a":1}`)}
	b := core.SourceConfig{Enabled: true, Reset: false, Auth: []byte(`{"a":1}`)}
	c := core.SourceConfig{Enabled: true, Reset: true, Auth: []byte(`{"a":1}`)}
	if !sourceConfigEqual(a, b) {
		t.Fatal("expected identical config values to compare equal")
	}
	if sourceConfigEqual(a, c) {
		t.Fatal("expected reset mismatch to compare unequal")
	}
}

// Verifies shouldIndexSource keeps full passes unconditional, indexes non-incremental sources on incremental passes, and skips unchanged incremental sources.
func TestShouldIndexSource(t *testing.T) {
	store := &spySourceIndexer{lastIndexed: map[string]time.Time{"inc": time.Now()}}
	if !shouldIndexSource(true, store, activeSource{
		desc: registry.Descriptor{Name: "plain", IndexGlobally: true},
		src:  &indexerTestSource{name: "plain"},
	}) {
		t.Fatal("expected full pass to index every source")
	}
	if !shouldIndexSource(false, store, activeSource{
		desc: registry.Descriptor{Name: "plain", IndexGlobally: true},
		src:  &indexerTestSource{name: "plain"},
	}) {
		t.Fatal("expected incremental pass to index non-incremental sources")
	}
	if shouldIndexSource(false, store, activeSource{
		desc: registry.Descriptor{Name: "inc", IndexGlobally: true},
		src:  &incrementalIndexerTestSource{indexerTestSource: indexerTestSource{name: "inc"}, changed: false},
	}) {
		t.Fatal("expected unchanged incremental source to be skipped")
	}
}

// Verifies indexSourceEntries treats streaming source errors as non-cancel failures and preserves earlier indexed batches.
func TestIndexSourceEntries_streamingError(t *testing.T) {
	store := &spySourceIndexer{}
	keys, completed, err := indexSourceEntries(context.Background(), store, activeSource{
		desc: registry.Descriptor{Name: "streamed", IndexGlobally: true},
		src: &streamingIndexerTestSource{
			name: "streamed",
			batches: [][]core.SearchEntry{
				{{Source: "streamed", SourceID: "1", ContentType: "x", Title: "one", Content: "one"}},
			},
			err: errors.New("boom"),
		},
	})
	if !completed {
		t.Fatal("expected non-cancel stream errors to leave the overall pass complete")
	}
	if err == nil {
		t.Fatal("expected stream error to be returned")
	}
	if len(store.indexed) != 1 || len(keys) != 0 {
		t.Fatalf("expected earlier batch to be indexed and prune keys to be discarded, got indexed=%d keys=%d", len(store.indexed), len(keys))
	}
}
