package browser_history

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
)

// Verifies enable command persists browser selection and enables the source in config.
func TestRunEnable_success(t *testing.T) {
	dataDir := t.TempDir()
	oldDaemonStatPath := daemonStatPath
	oldDaemonSignalProcess := daemonSignalProcess
	daemonStatPath = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	daemonSignalProcess = func(int, syscall.Signal) error { return nil }
	defer func() {
		daemonStatPath = oldDaemonStatPath
		daemonSignalProcess = oldDaemonSignalProcess
	}()

	stdout, _ := captureOutput(t, func() {
		RunEnable(dataDir, "chrome")
	})

	cfg := loadBrowserHistoryConfig(dataDir)
	if cfg.Browser != "chrome" {
		t.Fatalf("browser = %q", cfg.Browser)
	}
	sc := core.LoadConfig(dataDir).Sources["browser_history"]
	if !sc.Enabled {
		t.Fatal("expected source enabled")
	}
	if !strings.Contains(stdout, "browser_history: enabled (chrome)") {
		t.Fatalf("expected enabled message, got %q", stdout)
	}
	if !strings.Contains(stdout, "Start the daemon") {
		t.Fatalf("expected start-daemon message, got %q", stdout)
	}
}

// Verifies enable command prints the immediate-index message when the daemon is running and accepts the reindex signal.
func TestRunEnable_runningDaemon(t *testing.T) {
	dataDir := t.TempDir()
	oldDaemonStatPath := daemonStatPath
	oldDaemonLaunchctlList := daemonLaunchctlList
	oldDaemonSignalProcess := daemonSignalProcess
	daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
	daemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 123;}`), nil }
	daemonSignalProcess = func(pid int, signal syscall.Signal) error {
		if pid != 123 || signal != syscall.SIGUSR1 {
			t.Fatalf("signal args = (%d,%v)", pid, signal)
		}
		return nil
	}
	defer func() {
		daemonStatPath = oldDaemonStatPath
		daemonLaunchctlList = oldDaemonLaunchctlList
		daemonSignalProcess = oldDaemonSignalProcess
	}()

	stdout, _ := captureOutput(t, func() {
		RunEnable(dataDir, "chrome")
	})
	if !strings.Contains(stdout, "Indexing will begin shortly.") {
		t.Fatalf("expected immediate-index message, got %q", stdout)
	}
}

// Verifies enable command falls back to the next-refresh message when the daemon is running but signaling fails.
func TestRunEnable_runningDaemonSignalFailure(t *testing.T) {
	dataDir := t.TempDir()
	oldDaemonStatPath := daemonStatPath
	oldDaemonLaunchctlList := daemonLaunchctlList
	oldDaemonSignalProcess := daemonSignalProcess
	daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
	daemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 321;}`), nil }
	daemonSignalProcess = func(int, syscall.Signal) error { return errors.New("kill failed") }
	defer func() {
		daemonStatPath = oldDaemonStatPath
		daemonLaunchctlList = oldDaemonLaunchctlList
		daemonSignalProcess = oldDaemonSignalProcess
	}()

	stdout, _ := captureOutput(t, func() {
		RunEnable(dataDir, "chrome")
	})
	if !strings.Contains(stdout, "next refresh cycle") {
		t.Fatalf("expected next-refresh message, got %q", stdout)
	}
}

// Verifies enable command with no args enables the source when browser is already configured.
func TestRunEnable_noArgWithExistingBrowser(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "brave", false)
	oldDaemonStatPath := daemonStatPath
	daemonStatPath = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	defer func() { daemonStatPath = oldDaemonStatPath }()

	stdout, _ := captureOutput(t, func() {
		RunEnable(dataDir, "")
	})

	sc := core.LoadConfig(dataDir).Sources["browser_history"]
	if !sc.Enabled {
		t.Fatal("expected source enabled")
	}
	if !strings.Contains(stdout, "browser_history: enabled (brave)") {
		t.Fatalf("expected enabled message, got %q", stdout)
	}
}

// Verifies disable command sets the source disabled in config without clearing browser selection.
func TestRunDisable(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "chrome", true)

	stdout, _ := captureOutput(t, func() {
		RunDisable(dataDir)
	})

	sc := core.LoadConfig(dataDir).Sources["browser_history"]
	if sc.Enabled {
		t.Fatal("expected source disabled")
	}
	if loadBrowserHistoryConfig(dataDir).Browser != "chrome" {
		t.Fatal("expected browser selection preserved")
	}
	if !strings.Contains(stdout, "browser_history: disabled") {
		t.Fatalf("expected disabled message, got %q", stdout)
	}
}

// Verifies reset command honors cancellation input and leaves configuration untouched.
func TestRunReset_cancel(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "brave", true)
	withStdin(t, "no\n", func() {
		RunReset(dataDir)
	})

	sc := core.LoadConfig(dataDir).Sources["browser_history"]
	if !sc.Enabled {
		t.Fatal("expected source to remain enabled after cancel")
	}
}

// Verifies reset command deletes snapshot files and clears enabled/auth config on confirmation.
func TestRunReset_confirmed(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "chrome", true)
	snapshotPath := filepath.Join(dataDir, "browser_history.db")
	if err := os.WriteFile(snapshotPath, []byte("db"), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	seedBrowserSearchIndex(t, dataDir)

	withStdin(t, "yes\n", func() {
		RunReset(dataDir)
	})

	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Fatalf("expected snapshot removed, stat err=%v", err)
	}
	sc := core.LoadConfig(dataDir).Sources["browser_history"]
	if sc.Enabled || len(sc.Auth) != 0 {
		t.Fatalf("reset config = %+v", sc)
	}
	assertBrowserSearchSourceCount(t, dataDir, "browser_history", 0)
	assertBrowserSearchSourceCount(t, dataDir, "notebook", 1)
}

// Verifies reset command prints warnings when snapshot cleanup or config persistence fails.
func TestRunReset_warningPaths(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "chrome", true)

	oldNewResetSource := newResetSource
	oldUpdateSourceConfig := updateSourceConfig
	updateSourceConfig = func(string, string, func(*core.SourceConfig)) error {
		return assertErr("update failed")
	}
	defer func() {
		newResetSource = oldNewResetSource
		updateSourceConfig = oldUpdateSourceConfig
	}()

	newResetSource = func(string) resetter { return resetterFunc(func(string) error { return assertErr("reset failed") }) }

	_, stderr := captureOutput(t, func() {
		withStdin(t, "yes\n", func() {
			RunReset(dataDir)
		})
	})
	if !strings.Contains(stderr, "warning during reset") || !strings.Contains(stderr, "reset failed") {
		t.Fatalf("expected reset warning, got %q", stderr)
	}
	if !strings.Contains(stderr, "could not update config.json") || !strings.Contains(stderr, "update failed") {
		t.Fatalf("expected config warning, got %q", stderr)
	}
}

// seedBrowserSearchIndex creates a minimal shared search index so browser_history reset can verify it clears only its own rows.
func seedBrowserSearchIndex(t *testing.T, dataDir string) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "search.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE search_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			source_id TEXT NOT NULL,
			content_type TEXT NOT NULL,
			title TEXT,
			content TEXT NOT NULL,
			metadata TEXT,
			timestamp DATETIME,
			UNIQUE(source, source_id, content_type)
		);
		INSERT INTO search_entries (source, source_id, content_type, title, content)
		VALUES
			('browser_history', 'url-1', 'browser_visit', 'Example', 'https://example.com'),
			('notebook', 'note-1', 'note_title', 'John Thomas', 'John Thomas');
	`); err != nil {
		t.Fatalf("seed search db: %v", err)
	}
}

// assertBrowserSearchSourceCount checks the remaining row count for one source after browser_history reset mutates search.db.
func assertBrowserSearchSourceCount(t *testing.T, dataDir, source string, want int) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "search.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = ?`, source).Scan(&got); err != nil {
		t.Fatalf("count search rows for %s: %v", source, err)
	}
	if got != want {
		t.Fatalf("search row count for %s = %d, want %d", source, got, want)
	}
}

// Verifies daemonPID returns zero when the plist is missing or launchctl reports no PID.
func TestDaemonPID(t *testing.T) {
	t.Run("plist missing", func(t *testing.T) {
		oldDaemonStatPath := daemonStatPath
		daemonStatPath = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
		defer func() { daemonStatPath = oldDaemonStatPath }()

		if pid := daemonPID(); pid != 0 {
			t.Fatalf("daemonPID() = %d, want 0", pid)
		}
	})

	t.Run("launchctl parse", func(t *testing.T) {
		oldDaemonStatPath := daemonStatPath
		oldDaemonLaunchctlList := daemonLaunchctlList
		daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
		daemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 789;}`), nil }
		defer func() {
			daemonStatPath = oldDaemonStatPath
			daemonLaunchctlList = oldDaemonLaunchctlList
		}()

		if pid := daemonPID(); pid != 789 {
			t.Fatalf("daemonPID() = %d, want 789", pid)
		}
	})

	t.Run("launchctl error", func(t *testing.T) {
		oldDaemonStatPath := daemonStatPath
		oldDaemonLaunchctlList := daemonLaunchctlList
		daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
		daemonLaunchctlList = func(string) ([]byte, error) { return nil, errors.New("launchctl failed") }
		defer func() {
			daemonStatPath = oldDaemonStatPath
			daemonLaunchctlList = oldDaemonLaunchctlList
		}()

		if pid := daemonPID(); pid != 0 {
			t.Fatalf("daemonPID() = %d, want 0", pid)
		}
	})
}

// Verifies parseLaunchctlPID extracts the numeric process ID or zero when absent.
func TestParseLaunchctlPID(t *testing.T) {
	if pid := parseLaunchctlPID(`{"PID" = 456;}`); pid != 456 {
		t.Fatalf("parseLaunchctlPID() = %d, want 456", pid)
	}
	if pid := parseLaunchctlPID(`{"Label" = "com.mcpyeahyouknowme.core";}`); pid != 0 {
		t.Fatalf("parseLaunchctlPID() = %d, want 0", pid)
	}
}

// Replaces stdin for one function call so CLI prompt handlers can be tested non-interactively.
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := w.Write([]byte(input)); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()
	fn()
}

// Reset calls the wrapped function so CLI tests can inject deterministic reset outcomes.
func (f resetterFunc) Reset(dataDir string) error { return f(dataDir) }

// resetterFunc adapts plain functions to the resetter interface used by RunReset.
type resetterFunc func(dataDir string) error

// fakeFileInfo supplies minimal os.FileInfo behavior so daemon-status tests can stub os.Stat successfully.
type fakeFileInfo struct{ name string }

// Name returns the fake file name for daemon-status tests.
func (f fakeFileInfo) Name() string { return f.name }

// Size returns zero because daemon-status tests only care that the file exists.
func (f fakeFileInfo) Size() int64 { return 0 }

// Mode returns a regular-file mode because daemon-status tests only care that stat succeeds.
func (f fakeFileInfo) Mode() os.FileMode { return 0 }

// ModTime returns the zero time because daemon-status tests do not inspect timestamps.
func (f fakeFileInfo) ModTime() (t time.Time) { return t }

// IsDir reports false because daemon-status tests stub a plist file rather than a directory.
func (f fakeFileInfo) IsDir() bool { return false }

// Sys returns nil because daemon-status tests do not use platform-specific stat data.
func (f fakeFileInfo) Sys() interface{} { return nil }
