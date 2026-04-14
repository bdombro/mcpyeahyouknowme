package browser_history

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
)

// Verifies configuration detection only succeeds when a supported browser is stored.
func TestIsConfigured(t *testing.T) {
	dataDir := t.TempDir()
	if IsConfigured(dataDir) {
		t.Fatal("expected unconfigured source")
	}

	saveTestConfig(t, dataDir, "chrome", true)
	if !IsConfigured(dataDir) {
		t.Fatal("expected configured source")
	}

	saveTestConfig(t, dataDir, "unsupported", true)
	if IsConfigured(dataDir) {
		t.Fatal("expected unsupported browser to be unconfigured")
	}
}

// Verifies basic source identity and lifecycle no-op methods remain stable.
func TestSourceMethods(t *testing.T) {
	src := NewSource(t.TempDir())
	if src.Name() != "browser_history" {
		t.Fatalf("Name() = %q", src.Name())
	}
	if src.Description() != "Browser History" {
		t.Fatalf("Description() = %q", src.Description())
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

// Verifies browser_history config save/load round trips canonical browser names.
func TestSaveLoadBrowserHistoryConfig(t *testing.T) {
	dataDir := t.TempDir()
	if err := saveBrowserHistoryConfig(dataDir, BrowserHistoryConfig{Browser: " BrAvE "}); err != nil {
		t.Fatalf("saveBrowserHistoryConfig(): %v", err)
	}
	cfg := loadBrowserHistoryConfig(dataDir)
	if cfg.Browser != "brave" {
		t.Fatalf("loaded browser = %q", cfg.Browser)
	}
}

// Verifies malformed auth bytes safely load as an empty config instead of propagating JSON errors.
func TestLoadBrowserHistoryConfig_invalidJSON(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{"sources":{"browser_history":{"enabled":true,"auth":123}}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if cfg := loadBrowserHistoryConfig(dataDir); cfg.Browser != "" {
		t.Fatalf("loadBrowserHistoryConfig() = %+v, want empty config", cfg)
	}
}

// Verifies saveBrowserHistoryConfig surfaces config write failures from the shared config helper.
func TestSaveBrowserHistoryConfig_updateError(t *testing.T) {
	oldUpdateSourceConfig := updateSourceConfig
	updateSourceConfig = func(string, string, func(*core.SourceConfig)) error {
		return assertErr("update failed")
	}
	defer func() { updateSourceConfig = oldUpdateSourceConfig }()

	if err := saveBrowserHistoryConfig(t.TempDir(), BrowserHistoryConfig{Browser: "chrome"}); err == nil {
		t.Fatal("expected config update error")
	}
}

// Verifies SearchEntries refreshes snapshots on first run, skips unchanged copies, and re-copies when the fallback interval expires.
func TestSearchEntries_refreshBehavior(t *testing.T) {
	dataDir := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "History")
	sourceDB := newHistoryDB(t, sourcePath)
	insertVisit(t, sourceDB, 1, 10, "https://example.com", "Example", chromeEpochOffsetMicros+1_000_000, 1)

	saveTestConfig(t, dataDir, "chrome", true)
	src := NewSource(dataDir)
	src.resolvePath = func(_ string) (string, error) { return sourcePath, nil }
	copyCount := 0
	origCopy := src.copySnapshot
	src.copySnapshot = func(sp, dp string) (sourceFileMeta, error) {
		copyCount++
		return origCopy(sp, dp)
	}

	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("first SearchEntries: %v", err)
	}
	if len(entries) != 1 || copyCount != 1 {
		t.Fatalf("first run entries=%d copyCount=%d", len(entries), copyCount)
	}

	src.fallbackRefresh = time.Hour
	entries, err = src.SearchEntries()
	if err != nil {
		t.Fatalf("second SearchEntries: %v", err)
	}
	if len(entries) != 1 || copyCount != 1 {
		t.Fatalf("second run entries=%d copyCount=%d", len(entries), copyCount)
	}

	src.fallbackRefresh = 0
	entries, err = src.SearchEntries()
	if err != nil {
		t.Fatalf("third SearchEntries: %v", err)
	}
	if len(entries) != 1 || copyCount != 2 {
		t.Fatalf("third run entries=%d copyCount=%d", len(entries), copyCount)
	}
}

// Verifies SearchEntries reports refresh, snapshot-open, and index-query failures instead of swallowing them.
func TestSearchEntries_errorPaths(t *testing.T) {
	t.Run("resolve error", func(t *testing.T) {
		dataDir := t.TempDir()
		saveTestConfig(t, dataDir, "chrome", true)

		src := NewSource(dataDir)
		src.resolvePath = func(_ string) (string, error) { return "", assertErr("resolve failed") }
		if _, err := src.SearchEntries(); err == nil {
			t.Fatal("expected resolve error")
		}
	})

	t.Run("copy error", func(t *testing.T) {
		dataDir := t.TempDir()
		sourcePath := filepath.Join(t.TempDir(), "History")
		sourceDB := newHistoryDB(t, sourcePath)
		insertVisit(t, sourceDB, 1, 10, "https://example.com", "Example", chromeEpochOffsetMicros+1_000_000, 1)
		saveTestConfig(t, dataDir, "chrome", true)

		src := NewSource(dataDir)
		src.resolvePath = func(_ string) (string, error) { return sourcePath, nil }
		src.copySnapshot = func(string, string) (sourceFileMeta, error) { return sourceFileMeta{}, assertErr("copy failed") }
		if _, err := src.SearchEntries(); err == nil {
			t.Fatal("expected copy error")
		}
	})

	t.Run("source stat error", func(t *testing.T) {
		dataDir := t.TempDir()
		saveTestConfig(t, dataDir, "chrome", true)

		src := NewSource(dataDir)
		src.resolvePath = func(_ string) (string, error) {
			parent := filepath.Join(t.TempDir(), "not-a-dir")
			if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
				t.Fatalf("write parent file: %v", err)
			}
			return filepath.Join(parent, "History"), nil
		}
		if _, err := src.SearchEntries(); err == nil {
			t.Fatal("expected source stat error")
		}
	})

	t.Run("open snapshot error", func(t *testing.T) {
		dataDir := t.TempDir()
		sourcePath := filepath.Join(t.TempDir(), "History")
		sourceDB := newHistoryDB(t, sourcePath)
		insertVisit(t, sourceDB, 1, 10, "https://example.com", "Example", chromeEpochOffsetMicros+1_000_000, 1)
		saveTestConfig(t, dataDir, "chrome", true)

		src := NewSource(dataDir)
		src.resolvePath = func(_ string) (string, error) { return sourcePath, nil }
		src.openSnapshotDB = func(string) (*sql.DB, error) { return nil, assertErr("open failed") }
		if _, err := src.SearchEntries(); err == nil {
			t.Fatal("expected open snapshot error")
		}
	})

	t.Run("index rows error", func(t *testing.T) {
		dataDir := t.TempDir()
		sourcePath := filepath.Join(t.TempDir(), "History")
		sourceDB := newHistoryDB(t, sourcePath)
		insertVisit(t, sourceDB, 1, 10, "https://example.com", "Example", chromeEpochOffsetMicros+1_000_000, 1)
		saveTestConfig(t, dataDir, "chrome", true)

		src := NewSource(dataDir)
		src.resolvePath = func(_ string) (string, error) { return sourcePath, nil }
		src.openSnapshotDB = func(string) (*sql.DB, error) {
			dbPath := filepath.Join(t.TempDir(), "broken.db")
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			if _, err := db.Exec(`CREATE TABLE urls (id INTEGER PRIMARY KEY, url TEXT NOT NULL, title TEXT, visit_count TEXT, last_visit_time INTEGER NOT NULL DEFAULT 0);`); err != nil {
				t.Fatalf("create urls: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO urls (id, url, title, visit_count, last_visit_time) VALUES (1, 'https://example.com', 'Example', 'oops', ?);`, chromeEpochOffsetMicros+1_000_000); err != nil {
				t.Fatalf("insert malformed row: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db, nil
		}
		if _, err := src.SearchEntries(); err == nil {
			t.Fatal("expected listIndexRows error")
		}
	})
}

// Verifies SearchEntries returns no rows when browser_history is disabled or unconfigured.
func TestSearchEntries_noConfig(t *testing.T) {
	src := NewSource(t.TempDir())
	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries(): %v", err)
	}
	if entries != nil {
		t.Fatalf("entries = %v, want nil", entries)
	}
}

// Verifies openSnapshotForRead returns a clear error before daemon created the snapshot.
func TestOpenSnapshotForRead_missingSnapshot(t *testing.T) {
	src := NewSource(t.TempDir())
	if _, err := src.openSnapshotForRead(); err == nil {
		t.Fatal("expected missing snapshot error")
	}
}

// Verifies openSnapshotForRead succeeds when the daemon snapshot file exists.
func TestOpenSnapshotForRead_success(t *testing.T) {
	dataDir := t.TempDir()
	db := newHistoryDB(t, filepath.Join(dataDir, "browser_history.db"))
	insertVisit(t, db, 1, 1, "https://example.com", "Example", chromeEpochOffsetMicros+1_000, 1)

	src := NewSource(dataDir)
	opened, err := src.openSnapshotForRead()
	if err != nil {
		t.Fatalf("openSnapshotForRead(): %v", err)
	}
	_ = opened.Close()
}

// Verifies openSnapshotForRead returns non-NotExist stat errors verbatim for broken snapshot paths.
func TestOpenSnapshotForRead_statError(t *testing.T) {
	dataDir := t.TempDir()
	parentFile := filepath.Join(dataDir, "not-a-dir")
	if err := os.WriteFile(parentFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write parent file: %v", err)
	}

	src := NewSource(dataDir)
	src.snapshotPath = filepath.Join(parentFile, "browser_history.db")
	if _, err := src.openSnapshotForRead(); err == nil {
		t.Fatal("expected stat error")
	}
}

// Verifies info lines reflect disabled, unconfigured, and configured enabled states.
func TestInfoLines(t *testing.T) {
	dataDir := t.TempDir()
	lines := InfoLines(dataDir)
	if len(lines) != 0 {
		t.Fatalf("disabled: expected no lines, got %v", lines)
	}

	saveRawSourceConfig(t, dataDir, true, nil)
	lines = InfoLines(dataDir)
	if len(lines) != 1 || !strings.Contains(lines[0], "Hint") {
		t.Fatalf("unconfigured enabled lines = %v", lines)
	}

	saveTestConfig(t, dataDir, "brave", true)
	lines = InfoLines(dataDir)
	if len(lines) < 1 || !strings.Contains(lines[0], "Browser:") {
		t.Fatalf("enabled lines = %v", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "Status:") {
			t.Fatalf("Status line should not appear in InfoLines, got: %q", l)
		}
	}

	snapshotPath := filepath.Join(dataDir, "browser_history.db")
	if err := os.WriteFile(snapshotPath, []byte("db"), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	lines = InfoLines(dataDir)
	if len(lines) < 1 || !strings.Contains(lines[0], "Browser:") {
		t.Fatalf("expected browser line after snapshot written, got %v", lines)
	}
}

// Verifies HasChangesSince checks snapshot and WAL mtimes so incremental indexing can skip unchanged snapshots.
func TestSource_HasChangesSince(t *testing.T) {
	dataDir := t.TempDir()
	src := NewSource(dataDir)
	if !src.HasChangesSince(time.Time{}) {
		t.Fatal("expected zero watermark to force indexing")
	}
	if !src.HasChangesSince(time.Now()) {
		t.Fatal("expected missing snapshot files to trigger indexing")
	}

	if err := os.WriteFile(src.snapshotPath, []byte("db"), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	if err := os.WriteFile(src.snapshotPath+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(src.snapshotPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes snapshot: %v", err)
	}
	if err := os.Chtimes(src.snapshotPath+"-wal", newTime, newTime); err != nil {
		t.Fatalf("chtimes wal: %v", err)
	}

	if src.HasChangesSince(time.Now()) {
		t.Fatal("expected future watermark to skip unchanged snapshot")
	}
	if !src.HasChangesSince(time.Now().Add(-90 * time.Minute)) {
		t.Fatal("expected WAL change to trigger browser history reindex")
	}
}

// assertErr builds deterministic errors for compact source error-path assertions.
func assertErr(msg string) error { return &testErr{msg: msg} }

// testErr stores predictable error strings in tests.
type testErr struct{ msg string }

// Error returns the test error message.
func (e *testErr) Error() string { return e.msg }
