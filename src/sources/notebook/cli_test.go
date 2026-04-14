package notebook

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

	_ "modernc.org/sqlite"
)

// Verifies RunReset leaves notebook configuration untouched when the confirmation is declined.
func TestRunReset_cancel(t *testing.T) {
	dataDir := t.TempDir()
	configData, err := marshalConfig(NotebookConfig{Dirs: []string{"/tmp/notes"}})
	if err != nil {
		t.Fatalf("marshalConfig: %v", err)
	}
	if err := core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		sc.Enabled = true
		sc.Auth = configData
	}); err != nil {
		t.Fatalf("UpdateSourceConfig: %v", err)
	}

	withNotebookStdin(t, "no\n", func() {
		RunReset(dataDir)
	})

	sc := core.LoadConfig(dataDir).Sources["notebook"]
	if !sc.Enabled || len(sc.Auth) == 0 {
		t.Fatalf("expected notebook config to remain set, got %+v", sc)
	}
}

// Verifies RunReset clears notebook DB/config and removes notebook rows from the shared search index.
func TestRunReset_confirmed(t *testing.T) {
	dataDir := t.TempDir()
	configData, err := marshalConfig(NotebookConfig{Dirs: []string{"/tmp/notes"}})
	if err != nil {
		t.Fatalf("marshalConfig: %v", err)
	}
	if err := core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		sc.Enabled = true
		sc.Auth = configData
	}); err != nil {
		t.Fatalf("UpdateSourceConfig: %v", err)
	}
	for _, rel := range []string{"notebook.db", "notebook.db-wal", "notebook.db-shm"} {
		if err := os.WriteFile(filepath.Join(dataDir, rel), []byte("seed"), 0644); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
	}
	seedNotebookSearchIndex(t, dataDir)

	withNotebookStdin(t, "yes\n", func() {
		RunReset(dataDir)
	})

	for _, rel := range []string{"notebook.db", "notebook.db-wal", "notebook.db-shm"} {
		if _, err := os.Stat(filepath.Join(dataDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err = %v", rel, err)
		}
	}
	sc := core.LoadConfig(dataDir).Sources["notebook"]
	if sc.Enabled || len(sc.Auth) != 0 {
		t.Fatalf("expected notebook config cleared, got %+v", sc)
	}
	assertSearchSourceCount(t, dataDir, "notebook", 0)
	assertSearchSourceCount(t, dataDir, "gsuite", 1)
}

// withNotebookStdin swaps stdin so notebook CLI tests can drive interactive confirmation deterministically.
func withNotebookStdin(t *testing.T, input string, fn func()) {
	t.Helper()

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	w.Close()
	fn()
}

// seedNotebookSearchIndex creates a minimal shared search index so RunReset can verify notebook rows are cleared without touching unrelated sources.
func seedNotebookSearchIndex(t *testing.T, dataDir string) {
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
			('notebook', 'note-1', 'note_title', 'John Thomas', 'John Thomas'),
			('gsuite', 'thread-1', 'email_thread_subject', 'Offer', 'Squarespace');
	`); err != nil {
		t.Fatalf("seed search db: %v", err)
	}
}

// Verifies RunAdd signals the daemon to reindex immediately when it is running.
func TestRunAdd_signalsDaemon(t *testing.T) {
	dataDir := t.TempDir()
	notesDir := filepath.Join(dataDir, "notes")
	os.MkdirAll(notesDir, 0755)

	oldStat := daemonStatPath
	oldList := daemonLaunchctlList
	oldSignal := daemonSignalProcess
	daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
	daemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 42;}`), nil }
	var signaled bool
	daemonSignalProcess = func(pid int, sig syscall.Signal) error {
		if pid != 42 || sig != syscall.SIGUSR1 {
			t.Fatalf("signal args = (%d,%v)", pid, sig)
		}
		signaled = true
		return nil
	}
	defer func() {
		daemonStatPath = oldStat
		daemonLaunchctlList = oldList
		daemonSignalProcess = oldSignal
	}()

	stdout := captureStdout(t, func() {
		RunAdd(dataDir, []string{notesDir})
	})
	if !signaled {
		t.Fatal("expected daemon to be signaled")
	}
	if !strings.Contains(stdout, "Indexing will begin shortly.") {
		t.Fatalf("expected immediate-index message, got %q", stdout)
	}
}

// Verifies RunAdd prints start-daemon hint when the daemon is not running.
func TestRunAdd_noDaemon(t *testing.T) {
	dataDir := t.TempDir()
	notesDir := filepath.Join(dataDir, "notes")
	os.MkdirAll(notesDir, 0755)

	oldStat := daemonStatPath
	daemonStatPath = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	defer func() { daemonStatPath = oldStat }()

	stdout := captureStdout(t, func() {
		RunAdd(dataDir, []string{notesDir})
	})
	if !strings.Contains(stdout, "Start the daemon") {
		t.Fatalf("expected start-daemon message, got %q", stdout)
	}
}

// Verifies RunAdd falls back to next-refresh message when signaling fails.
func TestRunAdd_signalFailure(t *testing.T) {
	dataDir := t.TempDir()
	notesDir := filepath.Join(dataDir, "notes")
	os.MkdirAll(notesDir, 0755)

	oldStat := daemonStatPath
	oldList := daemonLaunchctlList
	oldSignal := daemonSignalProcess
	daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
	daemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 99;}`), nil }
	daemonSignalProcess = func(int, syscall.Signal) error { return errors.New("kill failed") }
	defer func() {
		daemonStatPath = oldStat
		daemonLaunchctlList = oldList
		daemonSignalProcess = oldSignal
	}()

	stdout := captureStdout(t, func() {
		RunAdd(dataDir, []string{notesDir})
	})
	if !strings.Contains(stdout, "next refresh cycle") {
		t.Fatalf("expected next-refresh message, got %q", stdout)
	}
}

// Verifies RunRemove signals the daemon after removing a directory.
func TestRunRemove_signalsDaemon(t *testing.T) {
	dataDir := t.TempDir()
	notesDir := filepath.Join(dataDir, "notes")
	os.MkdirAll(notesDir, 0755)

	configData, _ := marshalConfig(NotebookConfig{Dirs: []string{notesDir}})
	core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		sc.Enabled = true
		sc.Auth = configData
	})

	oldStat := daemonStatPath
	oldList := daemonLaunchctlList
	oldSignal := daemonSignalProcess
	daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
	daemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 77;}`), nil }
	var signaled bool
	daemonSignalProcess = func(pid int, sig syscall.Signal) error {
		if pid != 77 || sig != syscall.SIGUSR1 {
			t.Fatalf("signal args = (%d,%v)", pid, sig)
		}
		signaled = true
		return nil
	}
	defer func() {
		daemonStatPath = oldStat
		daemonLaunchctlList = oldList
		daemonSignalProcess = oldSignal
	}()

	stdout := captureStdout(t, func() {
		RunRemove(dataDir, []string{notesDir})
	})
	if !signaled {
		t.Fatal("expected daemon to be signaled on remove")
	}
	if !strings.Contains(stdout, "Indexing will begin shortly.") {
		t.Fatalf("expected immediate-index message, got %q", stdout)
	}
}

// Verifies daemonPID returns zero when the plist is missing or launchctl reports no PID.
func TestDaemonPID(t *testing.T) {
	t.Run("plist missing", func(t *testing.T) {
		old := daemonStatPath
		daemonStatPath = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
		defer func() { daemonStatPath = old }()

		if pid := daemonPID(); pid != 0 {
			t.Fatalf("daemonPID() = %d, want 0", pid)
		}
	})

	t.Run("launchctl parse", func(t *testing.T) {
		oldStat := daemonStatPath
		oldList := daemonLaunchctlList
		daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
		daemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 789;}`), nil }
		defer func() {
			daemonStatPath = oldStat
			daemonLaunchctlList = oldList
		}()

		if pid := daemonPID(); pid != 789 {
			t.Fatalf("daemonPID() = %d, want 789", pid)
		}
	})

	t.Run("launchctl error", func(t *testing.T) {
		oldStat := daemonStatPath
		oldList := daemonLaunchctlList
		daemonStatPath = func(string) (os.FileInfo, error) { return fakeFileInfo{name: "plist"}, nil }
		daemonLaunchctlList = func(string) ([]byte, error) { return nil, errors.New("launchctl failed") }
		defer func() {
			daemonStatPath = oldStat
			daemonLaunchctlList = oldList
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

// captureStdout redirects os.Stdout into a pipe so CLI output can be asserted in tests.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

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

// assertSearchSourceCount checks the remaining search rows for one source after notebook reset mutates the shared index.
func assertSearchSourceCount(t *testing.T, dataDir, source string, want int) {
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

// Verifies RunEnable sets notebook source enabled in config.
func TestRunEnable(t *testing.T) {
	dir := t.TempDir()
	RunEnable(dir)
	if !core.LoadConfig(dir).Sources["notebook"].Enabled {
		t.Fatal("expected notebook enabled")
	}
}

// Verifies RunDisable sets notebook source disabled in config.
func TestRunDisable(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "notebook", true); err != nil {
		t.Fatal(err)
	}
	RunDisable(dir)
	if core.LoadConfig(dir).Sources["notebook"].Enabled {
		t.Fatal("expected notebook disabled")
	}
}
