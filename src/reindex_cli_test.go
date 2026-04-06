package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

// Verifies handleReindex signals the running daemon instead of starting a local reindex.
func TestHandleReindex_signalsRunningDaemon(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldSignalProcess := reindexSignalProcess
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 123 }
	reindexLocalRunner = func([]string) error {
		t.Fatal("expected local reindex to be skipped when daemon is running")
		return nil
	}
	reindexSignalProcess = func(pid int, signal syscall.Signal) error {
		if pid != 123 || signal != syscall.SIGUSR1 {
			t.Fatalf("signal args = (%d, %v)", pid, signal)
		}
		return nil
	}
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexSignalProcess = oldSignalProcess
		reindexLocalRunner = oldLocalRunner
	}()

	stdout := captureMainStdout(t, func() {
		if err := handleReindex(nil); err != nil {
			t.Fatalf("handleReindex: %v", err)
		}
	})
	if !strings.Contains(stdout, "Signaled core daemon (PID 123) to reindex.") {
		t.Fatalf("unexpected stdout: %q", stdout)
	}
}

// Verifies handleReindex rejects legacy arguments now that every reindex is a full clear-and-rebuild.
func TestHandleReindex_rejectsArguments(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 123 }
	reindexLocalRunner = func([]string) error {
		t.Fatal("expected local reindex to be skipped when daemon is running")
		return nil
	}
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexLocalRunner = oldLocalRunner
	}()

	err := handleReindex([]string{"--clear"})
	if err == nil || !strings.Contains(err.Error(), "reindex does not accept arguments") {
		t.Fatalf("handleReindex(--clear) error = %v", err)
	}
}

// Verifies handleReindex returns a useful error when signaling the running
// daemon fails instead of silently falling back to duplicate local work.
func TestHandleReindex_signalFailure(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldSignalProcess := reindexSignalProcess
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 123 }
	reindexLocalRunner = func([]string) error {
		t.Fatal("expected local reindex to be skipped after signal failure")
		return nil
	}
	reindexSignalProcess = func(int, syscall.Signal) error { return errors.New("kill failed") }
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexSignalProcess = oldSignalProcess
		reindexLocalRunner = oldLocalRunner
	}()

	err := handleReindex(nil)
	if err == nil || !strings.Contains(err.Error(), "signal core daemon reindex: kill failed") {
		t.Fatalf("handleReindex() error = %v", err)
	}
}

// Verifies handleReindex falls back to the standalone reindex path when no daemon is running.
func TestHandleReindex_runsLocalWhenDaemonStopped(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 0 }
	localCalled := false
	reindexLocalRunner = func(args []string) error {
		localCalled = true
		if len(args) != 0 {
			t.Fatalf("local args = %v", args)
		}
		return nil
	}
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexLocalRunner = oldLocalRunner
	}()

	if err := handleReindex(nil); err != nil {
		t.Fatalf("handleReindex: %v", err)
	}
	if !localCalled {
		t.Fatal("expected local reindex runner to be called")
	}
}

// Verifies runLocalReindex clears the existing search DB before rebuilding from local source data.
func TestRunLocalReindex_clearsAndRebuilds(t *testing.T) {
	dir := t.TempDir()
	seedStore, err := NewSearchStore(dir)
	if err != nil {
		t.Fatalf("NewSearchStore: %v", err)
	}
	if err := seedStore.IndexEntries(seedSearchEntries()); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}
	seedStore.UpdateSourceTimestamp("seed", time.Now())
	if err := seedStore.Close(); err != nil {
		t.Fatalf("Close seedStore: %v", err)
	}

	oldDataDir := reindexDataDir
	oldNewSearchStore := reindexNewSearchStore
	oldActiveSources := reindexActiveSources
	defer func() {
		reindexDataDir = oldDataDir
		reindexNewSearchStore = oldNewSearchStore
		reindexActiveSources = oldActiveSources
	}()

	reindexDataDir = func() string { return dir }
	reindexNewSearchStore = func(dir string) (*SearchStore, error) {
		return NewSearchStore(dir)
	}
	reindexActiveSources = func(string) []activeSource {
		return []activeSource{{
			desc: registry.Descriptor{Name: "stub", IndexGlobally: true},
			src: &testMCPSource{
				name: "stub",
				entries: []core.SearchEntry{{
					Source:      "stub",
					SourceID:    "fresh",
					ContentType: "message",
					Title:       "Fresh",
					Content:     "rebuilt entry",
				}},
			},
		}}
	}

	stderr := captureMainStderr(t, func() {
		if err := runLocalReindex(nil); err != nil {
			t.Fatalf("runLocalReindex: %v", err)
		}
	})
	if !strings.Contains(stderr, "Clearing existing index...") {
		t.Fatalf("expected clear message, got %q", stderr)
	}

	stats := ReadOnlySearchIndexStats(dir)
	if stats.Entries != 1 {
		t.Fatalf("expected rebuilt single entry, got %+v", stats)
	}
	if _, err := os.Stat(filepath.Join(dir, "search.db")); err != nil {
		t.Fatalf("expected rebuilt search.db: %v", err)
	}
}

// Verifies runLocalReindex uses StreamSearchEntries when the source implements StreamingSource.
func TestRunLocalReindex_streamingSource(t *testing.T) {
	dir := t.TempDir()

	oldDataDir := reindexDataDir
	oldNewSearchStore := reindexNewSearchStore
	oldActiveSources := reindexActiveSources
	defer func() {
		reindexDataDir = oldDataDir
		reindexNewSearchStore = oldNewSearchStore
		reindexActiveSources = oldActiveSources
	}()

	reindexDataDir = func() string { return dir }
	reindexNewSearchStore = func(d string) (*SearchStore, error) {
		return NewSearchStore(d)
	}
	reindexActiveSources = func(string) []activeSource {
		return []activeSource{{
			desc: registry.Descriptor{Name: "streamed", IndexGlobally: true},
			src: &streamingTestSource{
				name: "streamed",
				streamBatches: [][]core.SearchEntry{
					{{Source: "streamed", SourceID: "a", ContentType: "msg", Title: "A", Content: "alpha"}},
					{{Source: "streamed", SourceID: "b", ContentType: "msg", Title: "B", Content: "beta"}},
				},
			},
		}}
	}

	if err := runLocalReindex(nil); err != nil {
		t.Fatalf("runLocalReindex: %v", err)
	}

	stats := ReadOnlySearchIndexStats(dir)
	if stats.Entries != 2 {
		t.Fatalf("expected 2 streamed entries indexed, got %d", stats.Entries)
	}
}

// Verifies runLocalReindex reports search-store construction failures before trying to clear or index anything.
func TestRunLocalReindex_searchStoreError(t *testing.T) {
	oldDataDir := reindexDataDir
	oldNewSearchStore := reindexNewSearchStore
	defer func() {
		reindexDataDir = oldDataDir
		reindexNewSearchStore = oldNewSearchStore
	}()

	reindexDataDir = func() string { return t.TempDir() }
	reindexNewSearchStore = func(string) (*SearchStore, error) {
		return nil, errors.New("store boom")
	}

	err := runLocalReindex(nil)
	if err == nil || !strings.Contains(err.Error(), "search index unavailable: store boom") {
		t.Fatalf("runLocalReindex error = %v", err)
	}
}

// Verifies runLocalReindex reports clear failures before starting the rebuild loop.
func TestRunLocalReindex_clearError(t *testing.T) {
	dir := t.TempDir()

	oldDataDir := reindexDataDir
	oldNewSearchStore := reindexNewSearchStore
	defer func() {
		reindexDataDir = oldDataDir
		reindexNewSearchStore = oldNewSearchStore
	}()

	reindexDataDir = func() string { return dir }
	reindexNewSearchStore = func(dir string) (*SearchStore, error) {
		store, err := NewSearchStore(dir)
		if err != nil {
			return nil, err
		}
		if err := store.Close(); err != nil {
			return nil, err
		}
		return store, nil
	}

	err := runLocalReindex(nil)
	if err == nil || !strings.Contains(err.Error(), "clear search index") {
		t.Fatalf("runLocalReindex error = %v", err)
	}
}

// captureMainStdout captures stdout for one function call so CLI tests can assert on user-facing messages.
func captureMainStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}

// captureMainStderr captures stderr for one function call so CLI tests can assert on progress and error messages.
func captureMainStderr(t *testing.T, fn func()) string {
	t.Helper()

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(out)
}
