package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

type testMCPSource struct {
	name     string
	entries  []core.SearchEntry
	calls    int
	err      error
	onSearch func()
}

// Returns the source name so the indexing test double satisfies core.DataSource.
func (s *testMCPSource) Name() string { return s.name }

// Returns the display label so the indexing test double satisfies core.DataSource.
func (s *testMCPSource) Description() string { return s.name }

// Exposes no tools because this indexing test double only exercises SearchEntries behavior.
func (s *testMCPSource) RegisterTools(*server.MCPServer) {}

// Returns seeded search entries and increments a call counter so the test can verify indexing eligibility.
func (s *testMCPSource) SearchEntries() ([]core.SearchEntry, error) {
	s.calls++
	if s.onSearch != nil {
		s.onSearch()
	}
	return s.entries, s.err
}

// Satisfies the reset method required by the data-source interface without mutating test state.
func (s *testMCPSource) Reset(string) error { return nil }

// Closes nothing because this indexing test double owns no resources.
func (s *testMCPSource) Close() error { return nil }

type fakeIndexer struct {
	indexed  [][]core.SearchEntry
	pruned   []string
	updated  []string
	onUpdate func(string)
	onIndex  func()
	err      error
	last     map[string]time.Time
}

// Records indexed batches so the test can verify which sources were actually passed to the indexer.
func (f *fakeIndexer) IndexEntries(entries []core.SearchEntry) error {
	f.indexed = append(f.indexed, entries)
	if f.onIndex != nil {
		f.onIndex()
	}
	return f.err
}

// Records prune calls so the test double still satisfies the sourceIndexer contract after incremental-prune support was added.
func (f *fakeIndexer) PruneSourceKeys(source string, _ []indexKey) error {
	f.pruned = append(f.pruned, source)
	return f.err
}

// Records timestamp updates so the test can verify only globally indexed sources advance their watermark.
func (f *fakeIndexer) UpdateSourceTimestamp(source string, _ time.Time) {
	f.updated = append(f.updated, source)
	if f.onUpdate != nil {
		f.onUpdate(source)
	}
}

// Returns seeded source timestamps so incremental indexing tests can control skip decisions.
func (f *fakeIndexer) LastIndexed(source string) time.Time {
	if f.last == nil {
		return time.Time{}
	}
	return f.last[source]
}

type streamingTestSource struct {
	name          string
	streamBatches [][]core.SearchEntry
	calls         int
}

// Returns the source name so streaming tests satisfy the data-source interface.
func (s *streamingTestSource) Name() string { return s.name }

// Returns the source description because streaming tests only care about index behavior.
func (s *streamingTestSource) Description() string { return s.name }

// Registers no tools because streaming tests never exercise MCP tool exposure.
func (s *streamingTestSource) RegisterTools(*server.MCPServer) {}

// Returns no fallback entries because streaming tests should hit the batch emitter path.
func (s *streamingTestSource) SearchEntries() ([]core.SearchEntry, error) { return nil, nil }

// Emits the seeded streaming batches so tests can verify batch-wise indexing behavior.
func (s *streamingTestSource) StreamSearchEntries(emit func([]core.SearchEntry) error) error {
	s.calls++
	for _, batch := range s.streamBatches {
		if err := emit(batch); err != nil {
			return err
		}
	}
	return nil
}

// Resets nothing because the streaming test source owns no external resources.
func (s *streamingTestSource) Reset(string) error { return nil }

// Closes nothing because the streaming test source owns no external resources.
func (s *streamingTestSource) Close() error { return nil }

type incrementalTestSource struct {
	testMCPSource
	changed bool
}

// HasChangesSince returns the seeded change result so incremental tests can drive skip logic deterministically.
func (s *incrementalTestSource) HasChangesSince(time.Time) bool { return s.changed }

// Verifies stderr suppression drops startup writes and restores the original stream for later fatal errors.
func TestSuppressStderr_restoresOriginalStream(t *testing.T) {
	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("open stderr pipe: %v", err)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
		reader.Close()
		writer.Close()
	})

	restore := suppressStderr()
	if _, err := fmt.Fprint(os.Stderr, "hidden"); err != nil {
		t.Fatalf("write suppressed stderr: %v", err)
	}
	restore()

	if _, err := fmt.Fprint(os.Stderr, "visible"); err != nil {
		t.Fatalf("write restored stderr: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stderr pipe writer: %v", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	if string(got) != "visible" {
		t.Fatalf("expected only restored stderr output, got %q", string(got))
	}
}

// Verifies stderr suppression leaves stderr untouched when the discard target cannot be opened.
func TestSuppressStderrWithOpen_openFailureKeepsOriginalStream(t *testing.T) {
	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("open stderr pipe: %v", err)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
		reader.Close()
		writer.Close()
	})

	restore := suppressStderrWithOpen(func() (*os.File, error) {
		return nil, errors.New("open failed")
	})
	if _, err := fmt.Fprint(os.Stderr, "visible"); err != nil {
		t.Fatalf("write unsuppressed stderr: %v", err)
	}
	restore()
	if err := writer.Close(); err != nil {
		t.Fatalf("close stderr pipe writer: %v", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	if string(got) != "visible" {
		t.Fatalf("expected original stderr output when suppression fails, got %q", string(got))
	}
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

	indexSources(context.Background(), store, []activeSource{
		{
			desc: registry.Descriptor{Name: "indexed", IndexGlobally: true},
			src:  indexedSrc,
		},
		{
			desc: registry.Descriptor{Name: "ondemand", IndexGlobally: false},
			src:  onDemandSrc,
		},
	}, true)

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

// Verifies indexSources stops before the next source when the context is canceled between source passes.
func TestIndexSources_stopsWhenContextCancelled(t *testing.T) {
	firstSrc := &testMCPSource{
		name:    "first",
		entries: []core.SearchEntry{{Source: "first", SourceID: "1", ContentType: "x", Title: "t", Content: "c"}},
	}
	secondSrc := &testMCPSource{
		name:    "second",
		entries: []core.SearchEntry{{Source: "second", SourceID: "2", ContentType: "x", Title: "t", Content: "c"}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeIndexer{
		onUpdate: func(source string) {
			if source == "first" {
				cancel()
			}
		},
	}

	completed := indexSources(ctx, store, []activeSource{
		{
			desc: registry.Descriptor{Name: "first", IndexGlobally: true},
			src:  firstSrc,
		},
		{
			desc: registry.Descriptor{Name: "second", IndexGlobally: true},
			src:  secondSrc,
		},
	}, true)

	if completed {
		t.Fatal("expected indexing to report cancellation")
	}
	if firstSrc.calls != 1 {
		t.Fatalf("expected first source SearchEntries to be called once, got %d", firstSrc.calls)
	}
	if secondSrc.calls != 0 {
		t.Fatalf("expected second source SearchEntries to be skipped after cancellation, got %d", secondSrc.calls)
	}
	if len(store.indexed) != 1 {
		t.Fatalf("expected one indexed batch before cancellation, got %d", len(store.indexed))
	}
}

// Verifies indexSources treats a nil context like Background so callers can omit cancellation when none is needed.
func TestIndexSources_nilContext(t *testing.T) {
	src := &testMCPSource{
		name:    "indexed",
		entries: []core.SearchEntry{{Source: "indexed", SourceID: "1", ContentType: "x", Title: "t", Content: "c"}},
	}
	store := &fakeIndexer{}
	var nilCtx context.Context

	completed := indexSources(nilCtx, store, []activeSource{{
		desc: registry.Descriptor{Name: "indexed", IndexGlobally: true},
		src:  src,
	}}, true)

	if !completed {
		t.Fatal("expected nil context to behave like a normal background run")
	}
	if src.calls != 1 {
		t.Fatalf("expected source SearchEntries to be called once, got %d", src.calls)
	}
}

// Verifies indexSources continues past source read failures instead of aborting the full run.
func TestIndexSources_searchEntriesError(t *testing.T) {
	badSrc := &testMCPSource{name: "bad", err: errors.New("boom")}
	goodSrc := &testMCPSource{
		name:    "good",
		entries: []core.SearchEntry{{Source: "good", SourceID: "1", ContentType: "x", Title: "t", Content: "c"}},
	}
	store := &fakeIndexer{}

	completed := indexSources(context.Background(), store, []activeSource{
		{desc: registry.Descriptor{Name: "bad", IndexGlobally: true}, src: badSrc},
		{desc: registry.Descriptor{Name: "good", IndexGlobally: true}, src: goodSrc},
	}, true)

	if !completed {
		t.Fatal("expected source read errors to be non-fatal")
	}
	if len(store.indexed) != 1 || len(store.updated) != 1 || store.updated[0] != "good" {
		t.Fatalf("expected only the good source to be indexed, got indexed=%d updated=%v", len(store.indexed), store.updated)
	}
}

// Verifies indexSources continues past indexer failures so later sources still get a chance to index.
func TestIndexSources_indexEntriesError(t *testing.T) {
	firstSrc := &testMCPSource{
		name:    "first",
		entries: []core.SearchEntry{{Source: "first", SourceID: "1", ContentType: "x", Title: "t", Content: "c"}},
	}
	secondSrc := &testMCPSource{
		name:    "second",
		entries: []core.SearchEntry{{Source: "second", SourceID: "2", ContentType: "x", Title: "t", Content: "c"}},
	}
	store := &fakeIndexer{err: errors.New("index failed")}

	completed := indexSources(context.Background(), store, []activeSource{
		{desc: registry.Descriptor{Name: "first", IndexGlobally: true}, src: firstSrc},
		{desc: registry.Descriptor{Name: "second", IndexGlobally: true}, src: secondSrc},
	}, true)

	if !completed {
		t.Fatal("expected indexer errors to be non-fatal for the overall loop")
	}
	if len(store.indexed) != 2 {
		t.Fatalf("expected both batches to be attempted, got %d", len(store.indexed))
	}
	if len(store.updated) != 0 {
		t.Fatalf("expected no timestamp updates on index errors, got %v", store.updated)
	}
}

// Verifies indexSources exits immediately when the context is already canceled before the loop begins.
func TestIndexSources_preCanceledContext(t *testing.T) {
	src := &testMCPSource{
		name:    "indexed",
		entries: []core.SearchEntry{{Source: "indexed", SourceID: "1", ContentType: "x", Title: "t", Content: "c"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	completed := indexSources(ctx, &fakeIndexer{}, []activeSource{{
		desc: registry.Descriptor{Name: "indexed", IndexGlobally: true},
		src:  src,
	}}, true)

	if completed {
		t.Fatal("expected pre-canceled context to stop indexing immediately")
	}
	if src.calls != 0 {
		t.Fatalf("expected source not to be touched, got %d SearchEntries calls", src.calls)
	}
}

// Verifies indexSources stops before indexing when cancellation arrives after SearchEntries returns.
func TestIndexSources_canceledAfterSearchEntries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &testMCPSource{
		name:    "indexed",
		entries: []core.SearchEntry{{Source: "indexed", SourceID: "1", ContentType: "x", Title: "t", Content: "c"}},
		onSearch: func() {
			cancel()
		},
	}
	store := &fakeIndexer{}

	completed := indexSources(ctx, store, []activeSource{{
		desc: registry.Descriptor{Name: "indexed", IndexGlobally: true},
		src:  src,
	}}, true)

	if completed {
		t.Fatal("expected cancellation after SearchEntries to stop indexing")
	}
	if len(store.indexed) != 0 {
		t.Fatalf("expected no indexing after cancellation, got %d batches", len(store.indexed))
	}
}

// Verifies indexSources stops and returns false when context is canceled mid-stream between batches.
func TestIndexSources_streamingCancelMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	batch1 := []core.SearchEntry{{Source: "streamed", SourceID: "1", ContentType: "x", Title: "one", Content: "one"}}
	batch2 := []core.SearchEntry{{Source: "streamed", SourceID: "2", ContentType: "x", Title: "two", Content: "two"}}
	src := &streamingTestSource{
		name:          "streamed",
		streamBatches: [][]core.SearchEntry{batch1, batch2},
	}
	// Intercept IndexEntries to cancel after the first batch so the second is never emitted.
	store := &fakeIndexer{}
	store.onIndex = func() {
		cancel()
	}

	completed := indexSources(ctx, store, []activeSource{{
		desc: registry.Descriptor{Name: "streamed", IndexGlobally: true},
		src:  src,
	}}, true)

	if completed {
		t.Fatal("expected indexSources to return false after mid-stream cancel")
	}
	if len(store.indexed) != 1 {
		t.Fatalf("expected only the first batch before cancel, got %d batches", len(store.indexed))
	}
}

// Verifies indexSources consumes streamed batches without first building one large source slice.
func TestIndexSources_streamingSource(t *testing.T) {
	src := &streamingTestSource{
		name: "streamed",
		streamBatches: [][]core.SearchEntry{
			{{Source: "streamed", SourceID: "1", ContentType: "x", Title: "one", Content: "one"}},
			{{Source: "streamed", SourceID: "2", ContentType: "x", Title: "two", Content: "two"}},
		},
	}
	store := &fakeIndexer{}

	completed := indexSources(context.Background(), store, []activeSource{{
		desc: registry.Descriptor{Name: "streamed", IndexGlobally: true},
		src:  src,
	}}, true)

	if !completed {
		t.Fatal("expected streaming indexing to complete")
	}
	if src.calls != 1 {
		t.Fatalf("expected one stream pass, got %d", src.calls)
	}
	if len(store.indexed) != 2 {
		t.Fatalf("expected two indexed batches, got %d", len(store.indexed))
	}
	if len(store.pruned) != 1 {
		t.Fatalf("expected one prune call, got %d", len(store.pruned))
	}
}

// Verifies incremental passes skip prune work and do nothing when the source reports no changes.
func TestIndexSources_incrementalSkipsUnchangedSource(t *testing.T) {
	src := &incrementalTestSource{
		testMCPSource: testMCPSource{
			name:    "notebook",
			entries: []core.SearchEntry{{Source: "notebook", SourceID: "1", ContentType: "note_title", Title: "t", Content: "c"}},
		},
		changed: false,
	}
	store := &fakeIndexer{last: map[string]time.Time{"notebook": time.Now().Add(-time.Minute)}}

	completed := indexSources(context.Background(), store, []activeSource{{
		desc: registry.Descriptor{Name: "notebook", IndexGlobally: true},
		src:  src,
	}}, false)

	if !completed {
		t.Fatal("expected incremental skip to complete cleanly")
	}
	if src.calls != 0 {
		t.Fatalf("expected unchanged source not to be read, got %d calls", src.calls)
	}
	if len(store.indexed) != 0 || len(store.pruned) != 0 || len(store.updated) != 0 {
		t.Fatalf("expected no incremental work for unchanged source, got indexed=%d pruned=%d updated=%d", len(store.indexed), len(store.pruned), len(store.updated))
	}
}

// Verifies incremental passes still index changed sources but defer prune work until a later full pass.
func TestIndexSources_incrementalIndexesChangedSourceWithoutPrune(t *testing.T) {
	src := &incrementalTestSource{
		testMCPSource: testMCPSource{
			name:    "notebook",
			entries: []core.SearchEntry{{Source: "notebook", SourceID: "1", ContentType: "note_title", Title: "t", Content: "c"}},
		},
		changed: true,
	}
	store := &fakeIndexer{last: map[string]time.Time{"notebook": time.Now().Add(-time.Minute)}}

	completed := indexSources(context.Background(), store, []activeSource{{
		desc: registry.Descriptor{Name: "notebook", IndexGlobally: true},
		src:  src,
	}}, false)

	if !completed {
		t.Fatal("expected changed source to index during incremental pass")
	}
	if src.calls != 1 {
		t.Fatalf("expected one source read, got %d", src.calls)
	}
	if len(store.indexed) != 1 {
		t.Fatalf("expected one indexed batch, got %d", len(store.indexed))
	}
	if len(store.pruned) != 0 {
		t.Fatalf("expected no prune on incremental pass, got %d", len(store.pruned))
	}
	if len(store.updated) != 1 {
		t.Fatalf("expected watermark update on incremental pass, got %d", len(store.updated))
	}
}
