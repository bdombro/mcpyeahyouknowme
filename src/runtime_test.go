package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	_ "modernc.org/sqlite"
)

type runtimeTestSource struct {
	resetCalled bool
}

// Returns the stub source name so runtime tests can register a minimal data source.
func (r *runtimeTestSource) Name() string { return "stub" }

// Returns the stub description so runtime tests satisfy the data-source interface.
func (r *runtimeTestSource) Description() string { return "Stub" }

// Registers no tools because runtime tests only exercise daemon lifecycle helpers.
func (r *runtimeTestSource) RegisterTools(core.ToolAdder) {}

// Returns no entries because runtime tests are focused on reset/start behavior, not indexing.
func (r *runtimeTestSource) SearchEntries() ([]core.SearchEntry, error) { return nil, nil }

// Marks resetCalled so runtime tests can verify the reset path invokes the source.
func (r *runtimeTestSource) Reset(string) error { r.resetCalled = true; return nil }

// Closes nothing because the runtime test stub owns no resources.
func (r *runtimeTestSource) Close() error { return nil }

// Verifies handleReset keeps the source config entry, clears indexed rows for that source, and disables it instead of deleting config state.
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

	searchDB, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open search db: %v", err)
	}
	t.Cleanup(func() { searchDB.Close() })
	searchStore, err := NewSearchStoreFromDB(searchDB)
	if err != nil {
		t.Fatalf("NewSearchStoreFromDB: %v", err)
	}
	stubMeta := json.RawMessage(`{"path":"stub.md"}`)
	otherMeta := json.RawMessage(`{"path":"other.md"}`)
	if err := searchStore.IndexEntries([]SearchEntry{
		{Source: "stub", SourceID: "stub#title", ContentType: "note_title", Title: "Stub", Content: "Stub", Metadata: stubMeta},
		{Source: "other", SourceID: "other#title", ContentType: "note_title", Title: "Other", Content: "Other", Metadata: otherMeta},
	}); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	cfg := core.LoadConfig(dir)
	handleReset(dir, "stub", &cfg, searchStore)

	if !src.resetCalled {
		t.Fatal("expected reset to be called")
	}
	var stubCount int
	if err := searchStore.db.QueryRow("SELECT COUNT(*) FROM search_entries WHERE source = 'stub'").Scan(&stubCount); err != nil {
		t.Fatalf("count stub rows: %v", err)
	}
	if stubCount != 0 {
		t.Fatalf("expected stub rows to be deleted, got %d", stubCount)
	}
	var otherCount int
	if err := searchStore.db.QueryRow("SELECT COUNT(*) FROM search_entries WHERE source = 'other'").Scan(&otherCount); err != nil {
		t.Fatalf("count other rows: %v", err)
	}
	if otherCount != 1 {
		t.Fatalf("expected other rows to remain, got %d", otherCount)
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

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	oldSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldSlog)

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

// Verifies the coordinator cancels the active run and starts one fresh rerun after the worker yields.
func TestIndexCoordinator_requestRestart(t *testing.T) {
	type run struct {
		ctx        context.Context
		clearFirst bool
		fullPass   bool
	}
	started := make(chan run, 2)
	released := make(chan struct{}, 1)
	coordinator := newIndexCoordinator(func(ctx context.Context, clearFirst, fullPass bool) {
		started <- run{ctx: ctx, clearFirst: clearFirst, fullPass: fullPass}
		<-released
	})

	coordinator.Request(false, false, false)
	firstRun := <-started

	coordinator.Request(true, true, true)

	select {
	case <-firstRun.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expected first run context to be canceled")
	}

	released <- struct{}{}

	select {
	case nextRun := <-started:
		if !nextRun.clearFirst {
			t.Fatal("expected restarted run to request a full clear")
		}
		if !nextRun.fullPass {
			t.Fatal("expected restarted run to be a full pass")
		}
	case <-time.After(time.Second):
		t.Fatal("expected restart run to begin after cancellation")
	}
}

// Verifies ticker-style requests do not cancel an active run and do not queue a second pass.
func TestIndexCoordinator_requestWhileRunning_noRestart(t *testing.T) {
	type run struct {
		ctx        context.Context
		clearFirst bool
		fullPass   bool
	}
	started := make(chan run, 1)
	released := make(chan struct{}, 1)
	coordinator := newIndexCoordinator(func(ctx context.Context, clearFirst, fullPass bool) {
		started <- run{ctx: ctx, clearFirst: clearFirst, fullPass: fullPass}
		<-released
	})

	coordinator.Request(false, false, false)
	firstRun := <-started

	coordinator.Request(false, true, true)

	select {
	case <-firstRun.ctx.Done():
		t.Fatal("expected non-restart request to leave current run alone")
	case <-time.After(50 * time.Millisecond):
	}

	released <- struct{}{}

	select {
	case <-started:
		t.Fatal("expected no queued restart for non-restart request")
	case <-time.After(50 * time.Millisecond):
	}
}

// Verifies nil-safe coordinator helpers return without panicking when no worker is configured.
func TestIndexCoordinator_nilSafety(_ *testing.T) {
	var nilCoordinator *indexCoordinator
	nilCoordinator.Request(false, false, false)
	nilCoordinator.Stop()

	coordinator := newIndexCoordinator(nil)
	coordinator.Request(false, false, false)
}

// Verifies Stop cancels the active run and clears pending restart state.
func TestIndexCoordinator_stop(t *testing.T) {
	type run struct {
		ctx        context.Context
		clearFirst bool
		fullPass   bool
	}
	started := make(chan run, 1)
	released := make(chan struct{}, 1)
	coordinator := newIndexCoordinator(func(ctx context.Context, clearFirst, fullPass bool) {
		started <- run{ctx: ctx, clearFirst: clearFirst, fullPass: fullPass}
		<-released
	})

	coordinator.Request(false, false, false)
	activeRun := <-started
	coordinator.restartPending = true
	coordinator.clearPending = true
	coordinator.fullPassPending = true

	stopped := make(chan struct{})
	go func() {
		coordinator.Stop()
		close(stopped)
	}()

	select {
	case <-activeRun.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expected Stop to cancel the active run")
	}

	released <- struct{}{}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("expected Stop to return after the worker exits")
	}
	if coordinator.restartPending {
		t.Fatal("expected Stop to clear pending restart state")
	}
	if coordinator.clearPending {
		t.Fatal("expected Stop to clear pending full-clear state")
	}
	if coordinator.fullPassPending {
		t.Fatal("expected Stop to clear pending full-pass state")
	}
}

// Verifies core.TrimLogFilePath keeps only the newest newline-aligned tail once the file grows past the threshold.
func TestTrimLogFilePath_keepsNewestTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	content := []byte("old-a\nold-b\nkeep-1\nkeep-2\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := core.TrimLogFilePath(path, 20, 15); err != nil {
		t.Fatalf("TrimLogFilePath: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if want := "keep-1\nkeep-2\n"; string(got) != want {
		t.Fatalf("core.TrimLogFilePath() = %q, want %q", string(got), want)
	}
}

// Verifies core.TrimLogFilePath leaves smaller files untouched so short-lived logs keep their full context.
func TestTrimLogFilePath_belowThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	content := []byte("keep-everything\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := core.TrimLogFilePath(path, int64(len(content)), 4); err != nil {
		t.Fatalf("core.TrimLogFilePath: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("core.TrimLogFilePath() modified file below threshold: got %q want %q", string(got), string(content))
	}
}

// Verifies core.TrimLogFilePath treats a missing log file as a no-op so first daemon startup does not emit an avoidable warning.
func TestTrimLogFilePath_missingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	if err := core.TrimLogFilePath(path, 1, 1); err != nil {
		t.Fatalf("core.TrimLogFilePath: %v", err)
	}
}

// Verifies trimLogFile uses the daemon thresholds so oversized core.log files keep only the newest tail on startup.
func TestTrimLogFile_keepsNewestTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core.log")
	oldChunk := bytes.Repeat([]byte("old-line\n"), (core.LogTrimThresholdBytes/9)+1)
	keepChunk := bytes.Repeat([]byte("keep-line\n"), (core.LogKeepTailBytes/10)+1)
	content := append(oldChunk, keepChunk...)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	trimLogFile(dir)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Contains(got, []byte("old-line\n")) {
		t.Fatal("expected old log lines to be trimmed")
	}
	if !bytes.Contains(got, []byte("keep-line\n")) {
		t.Fatal("expected newest log lines to remain")
	}
	if len(got) > int(core.LogKeepTailBytes) {
		t.Fatalf("expected trimmed file to be at most %d bytes, got %d", core.LogKeepTailBytes, len(got))
	}
}

// Verifies trimLogFile reports trim failures via slog so daemon startup surfaces filesystem issues instead of failing silently.
func TestTrimLogFile_reportsWarning(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	oldSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldSlog)

	trimLogFile(string([]byte{0}))

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out := make([]byte, 256)
	n, _ := r.Read(out)
	if got := string(out[:n]); got == "" {
		t.Fatal("expected trim warning on stderr")
	}
}

// Verifies core.TrimLogFilePath keeps the whole file when the requested tail is larger than the file itself.
func TestTrimLogFilePath_keepTailLargerThanFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	content := []byte("only-line\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := core.TrimLogFilePath(path, 1, 1024); err != nil {
		t.Fatalf("core.TrimLogFilePath: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("core.TrimLogFilePath() = %q, want %q", string(got), string(content))
	}
}

// Verifies core.TrimLogFilePath returns stat errors that are not simple missing-file cases.
func TestTrimLogFilePath_invalidPath(t *testing.T) {
	if err := core.TrimLogFilePath(string([]byte{0}), 1, 1); err == nil {
		t.Fatal("expected invalid path error")
	}
}

// Verifies core.TrimLogFilePath returns read errors when the path is not a regular file despite existing on disk.
func TestTrimLogFilePath_readError(t *testing.T) {
	if err := core.TrimLogFilePath(t.TempDir(), 0, 1); err == nil {
		t.Fatal("expected read error for directory path")
	}
}

// Verifies core.TrimLogFilePath returns open errors when the log file exists but cannot be read.
func TestTrimLogFilePath_openError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	content := []byte("line-one\nline-two\n")
	if err := os.WriteFile(path, content, 0000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := core.TrimLogFilePath(path, 1, 4); err == nil {
		t.Fatal("expected open error")
	}
}

// Verifies core.TrimLogFilePath returns rewrite errors when the log file cannot be reopened for truncation.
func TestTrimLogFilePath_reopenForWriteError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	content := []byte("line-one\nline-two\n")
	if err := os.WriteFile(path, content, 0400); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := core.TrimLogFilePath(path, 1, 4); err == nil {
		t.Fatal("expected reopen-for-write error")
	}
}

// Verifies core.TrimLogFilePath returns write errors from the final in-place rewrite so callers can warn without corrupting the file.
func TestTrimLogFilePath_writeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	content := []byte("old-a\nold-b\nkeep-1\nkeep-2\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := core.SetTrimLogWriteForTest(func(*os.File, []byte) error {
		return errors.New("write failed")
	})
	t.Cleanup(restore)

	if err := core.TrimLogFilePath(path, 20, 15); err == nil {
		t.Fatal("expected write error")
	}
}

// Verifies handleCoreSignal runs an immediate index pass for SIGUSR1 without stopping the daemon.
func TestHandleCoreSignal_reindex(t *testing.T) {
	running := map[string]context.CancelFunc{}
	indexCalled := false

	if stop := handleCoreSignal(syscall.SIGUSR1, running, nil, nil, func() { indexCalled = true }); stop {
		t.Fatal("expected daemon to keep running after SIGUSR1")
	}
	if !indexCalled {
		t.Fatal("expected reindex callback to run")
	}
}

// Verifies handleCoreSignal cancels running sources and stops the daemon for termination signals.
func TestHandleCoreSignal_shutdown(t *testing.T) {
	cancelled := false
	indexStopped := false
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := initSearchSchema(db); err != nil {
		t.Fatalf("initSearchSchema: %v", err)
	}
	searchStore := &SearchStore{db: db}
	running := map[string]context.CancelFunc{
		"stub": func() { cancelled = true },
	}

	if stop := handleCoreSignal(syscall.SIGTERM, running, searchStore, func() { indexStopped = true }, func() {}); !stop {
		t.Fatal("expected daemon to stop after SIGTERM")
	}
	if !indexStopped {
		t.Fatal("expected in-flight indexing to be canceled during shutdown")
	}
	if !cancelled {
		t.Fatal("expected running sources to be cancelled")
	}
	if err := db.Ping(); err == nil {
		t.Fatal("expected search store database to be closed on shutdown")
	}
}
