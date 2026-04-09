package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

// fakeResetSource simulates source reset behavior so reset tests can cover success and warning paths without depending on real source implementations.
type fakeResetSource struct {
	resetErr   error
	closeErr   error
	closePanic string
}

// Name returns a stable fake source name for reset CLI tests.
func (f *fakeResetSource) Name() string {
	return "fake"
}

// Description returns a human-readable label for reset CLI tests.
func (f *fakeResetSource) Description() string {
	return "fake"
}

// RegisterTools satisfies the core.DataSource interface for reset CLI tests that never exercise MCP registration.
func (f *fakeResetSource) RegisterTools(_ core.ToolAdder) {}

// SearchEntries returns no entries because reset CLI tests only care about reset and close side effects.
func (f *fakeResetSource) SearchEntries() ([]core.SearchEntry, error) {
	return nil, nil
}

// Reset returns the configured error so tests can drive warning output deterministically.
func (f *fakeResetSource) Reset(_ string) error {
	return f.resetErr
}

// Close returns the configured error so tests can verify cleanup warnings.
func (f *fakeResetSource) Close() error {
	if f.closePanic != "" {
		panic(f.closePanic)
	}
	return f.closeErr
}

// TestRunResetAll_cancelled stops before any destructive work when the user declines or stdin cannot provide a confirmation token.
func TestRunResetAll_cancelled(t *testing.T) {
	testCases := []struct {
		name  string
		input string
	}{
		{name: "declined", input: "no\n"},
		{name: "scan error", input: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oldStdin := resetAllStdin
			oldStdout := resetAllStdout
			oldStderr := resetAllStderr
			oldSearchReset := resetAllSearchReset
			oldDaemonPID := resetAllDaemonPID
			oldRestartDaemon := resetAllRestartDaemon
			defer func() {
				resetAllStdin = oldStdin
				resetAllStdout = oldStdout
				resetAllStderr = oldStderr
				resetAllSearchReset = oldSearchReset
				resetAllDaemonPID = oldDaemonPID
				resetAllRestartDaemon = oldRestartDaemon
			}()

			var stdout bytes.Buffer
			resetAllStdin = strings.NewReader(tc.input)
			resetAllStdout = &stdout
			resetAllStderr = &bytes.Buffer{}

			called := false
			resetAllSearchReset = func(string) error {
				called = true
				return nil
			}

			runResetAll(t.TempDir())

			if called {
				t.Fatal("runResetAll should not perform reset work when confirmation fails")
			}
			if got := stdout.String(); !strings.Contains(got, "Cancelled.") {
				t.Fatalf("runResetAll output = %q, want cancelled message", got)
			}
		})
	}
}

// TestRunResetAll_yesRunsReset clears data after a positive confirmation and reports the major reset steps.
func TestRunResetAll_yesRunsReset(t *testing.T) {
	oldStdin := resetAllStdin
	oldStdout := resetAllStdout
	oldStderr := resetAllStderr
	oldDescriptors := resetAllDescriptors
	oldSearchReset := resetAllSearchReset
	oldSaveConfig := resetAllSaveConfig
	oldDaemonPID := resetAllDaemonPID
	oldRestartDaemon := resetAllRestartDaemon
	defer func() {
		resetAllStdin = oldStdin
		resetAllStdout = oldStdout
		resetAllStderr = oldStderr
		resetAllDescriptors = oldDescriptors
		resetAllSearchReset = oldSearchReset
		resetAllSaveConfig = oldSaveConfig
		resetAllDaemonPID = oldDaemonPID
		resetAllRestartDaemon = oldRestartDaemon
	}()

	var stdout bytes.Buffer
	resetAllStdin = strings.NewReader("yes\n")
	resetAllStdout = &stdout
	resetAllStderr = &bytes.Buffer{}
	resetAllDaemonPID = func() int { return 4321 }
	resetAllRestartDaemon = func() error { return errors.New("restart failed") }
	resetAllDescriptors = func() []registry.Descriptor {
		return []registry.Descriptor{
			{
				Name: "fake",
				New: func(string) core.DataSource {
					return &fakeResetSource{}
				},
			},
		}
	}

	runResetAll(t.TempDir())

	got := stdout.String()
	for _, want := range []string{
		"This will reset ALL source connections and data. Are you sure? (yes/no): ",
		"  Reset fake",
		"  Reset search index",
		"  Reset config.json",
		"All connections and data reset.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("runResetAll output missing %q in:\n%s", want, got)
		}
	}
}

// TestDoResetAll_clearsSourceFilesSearchAndConfig removes real source-owned files, preserves unrelated cached assets, and normalizes config back to disabled sources.
func TestDoResetAll_clearsSourceFilesSearchAndConfig(t *testing.T) {
	oldStdout := resetAllStdout
	oldStderr := resetAllStderr
	oldDescriptors := resetAllDescriptors
	oldSearchReset := resetAllSearchReset
	oldSaveConfig := resetAllSaveConfig
	oldDaemonPID := resetAllDaemonPID
	oldRestartDaemon := resetAllRestartDaemon
	defer func() {
		resetAllStdout = oldStdout
		resetAllStderr = oldStderr
		resetAllDescriptors = oldDescriptors
		resetAllSearchReset = oldSearchReset
		resetAllSaveConfig = oldSaveConfig
		resetAllDaemonPID = oldDaemonPID
		resetAllRestartDaemon = oldRestartDaemon
	}()

	tmpDir := t.TempDir()
	resetAllStdout = &bytes.Buffer{}
	resetAllStderr = &bytes.Buffer{}
	resetAllDescriptors = func() []registry.Descriptor { return registry.All }
	resetAllDaemonPID = func() int { return 4321 }
	resetAllRestartDaemon = func() error { return errors.New("restart failed") }
	resetAllSaveConfig = func(dataDir string, cfg core.Config) error {
		return core.SaveConfig(dataDir, cfg)
	}

	seedPaths := []string{
		"messages.db",
		"messages.db-wal",
		"messages.db-shm",
		"whatsapp.db",
		"whatsapp.db-wal",
		"whatsapp.db-shm",
		"gsuite.db",
		"gsuite.db-wal",
		"gsuite.db-shm",
		"gsuite_token.json",
		"gsuite_email.txt",
		"browser_history.db",
		"browser_history.db-wal",
		"browser_history.db-shm",
		"notebook.db",
		"notebook.db-wal",
		"notebook.db-shm",
		"search.db",
		"search.db-wal",
		"search.db-shm",
	}
	for _, rel := range seedPaths {
		writeTestFile(t, filepath.Join(tmpDir, rel), "seed")
	}
	writeTestFile(t, filepath.Join(tmpDir, "cache", "tokenizer", "keep.txt"), "token-cache")

	err := core.SaveConfig(tmpDir, core.Config{
		Sources: map[string]core.SourceConfig{
			"whatsapp": {
				Enabled: true,
				Auth:    []byte(`{"token":"wa"}`),
			},
			"notebook": {
				Enabled: true,
				Auth:    []byte(`{"dirs":["/tmp/notes"]}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	doResetAll(tmpDir)

	for _, rel := range seedPaths {
		if _, err := os.Stat(filepath.Join(tmpDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err = %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "cache", "tokenizer", "keep.txt")); err != nil {
		t.Fatalf("expected cache/tokenizer/keep.txt to remain, stat err = %v", err)
	}

	cfg := core.LoadConfig(tmpDir)
	wantNames := core.KnownSources()
	if len(cfg.Sources) != len(wantNames) {
		t.Fatalf("LoadConfig returned %d sources, want %d", len(cfg.Sources), len(wantNames))
	}
	for _, name := range wantNames {
		sc, ok := cfg.Sources[name]
		if !ok {
			t.Fatalf("LoadConfig missing source %q", name)
		}
		if sc.Enabled {
			t.Fatalf("source %q should be disabled after reset", name)
		}
		if sc.Reset {
			t.Fatalf("source %q should not keep reset flag after reset", name)
		}
		if len(sc.Auth) != 0 {
			t.Fatalf("source %q should not keep auth after reset, got %q", name, string(sc.Auth))
		}
	}
}

// TestDoResetAll_logsWarnings reports source, search, and config failures while still finishing the overall reset flow.
func TestDoResetAll_logsWarnings(t *testing.T) {
	oldStdout := resetAllStdout
	oldStderr := resetAllStderr
	oldDescriptors := resetAllDescriptors
	oldSearchReset := resetAllSearchReset
	oldSaveConfig := resetAllSaveConfig
	oldDaemonPID := resetAllDaemonPID
	oldRestartDaemon := resetAllRestartDaemon
	defer func() {
		resetAllStdout = oldStdout
		resetAllStderr = oldStderr
		resetAllDescriptors = oldDescriptors
		resetAllSearchReset = oldSearchReset
		resetAllSaveConfig = oldSaveConfig
		resetAllDaemonPID = oldDaemonPID
		resetAllRestartDaemon = oldRestartDaemon
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	resetAllStdout = &stdout
	resetAllStderr = &stderr
	resetAllDaemonPID = func() int { return 4321 }
	resetAllRestartDaemon = func() error { return errors.New("restart failed") }
	resetAllDescriptors = func() []registry.Descriptor {
		return []registry.Descriptor{
			{
				Name: "broken",
				New: func(string) core.DataSource {
					return &fakeResetSource{
						resetErr: errors.New("reset failed"),
						closeErr: errors.New("close failed"),
					}
				},
			},
			{
				Name: "panic",
				New: func(string) core.DataSource {
					return &fakeResetSource{closePanic: "boom"}
				},
			},
		}
	}
	resetAllSearchReset = func(string) error {
		return errors.New("search failed")
	}
	resetAllSaveConfig = func(string, core.Config) error {
		return errors.New("config failed")
	}

	doResetAll(t.TempDir())

	gotErr := stderr.String()
	for _, want := range []string{
		"Warning: broken reset: reset failed",
		"Warning: broken close: close failed",
		"Warning: panic close panic: boom",
		"Warning: search index reset: search failed",
		"Warning: could not reset config.json: config failed",
		"Warning: could not restart daemon after reset: restart failed",
	} {
		if !strings.Contains(gotErr, want) {
			t.Fatalf("doResetAll stderr missing %q in:\n%s", want, gotErr)
		}
	}
	if got := stdout.String(); !strings.Contains(got, "All connections and data reset.") {
		t.Fatalf("doResetAll stdout = %q, want final summary", got)
	}
}

// TestDoResetAll_restartsDaemonWhenRunning verifies global reset restarts the running daemon so it reopens a fresh search index after file deletion.
func TestDoResetAll_restartsDaemonWhenRunning(t *testing.T) {
	oldStdout := resetAllStdout
	oldStderr := resetAllStderr
	oldDescriptors := resetAllDescriptors
	oldSearchReset := resetAllSearchReset
	oldSaveConfig := resetAllSaveConfig
	oldDaemonPID := resetAllDaemonPID
	oldRestartDaemon := resetAllRestartDaemon
	defer func() {
		resetAllStdout = oldStdout
		resetAllStderr = oldStderr
		resetAllDescriptors = oldDescriptors
		resetAllSearchReset = oldSearchReset
		resetAllSaveConfig = oldSaveConfig
		resetAllDaemonPID = oldDaemonPID
		resetAllRestartDaemon = oldRestartDaemon
	}()

	var stdout bytes.Buffer
	resetAllStdout = &stdout
	resetAllStderr = &bytes.Buffer{}
	resetAllDescriptors = func() []registry.Descriptor { return nil }
	resetAllSearchReset = func(string) error { return nil }
	resetAllSaveConfig = func(string, core.Config) error { return nil }
	resetAllDaemonPID = func() int { return 1234 }
	restarted := false
	resetAllRestartDaemon = func() error {
		restarted = true
		return nil
	}

	doResetAll(t.TempDir())

	if !restarted {
		t.Fatal("expected daemon restart when PID is present")
	}
	if got := stdout.String(); !strings.Contains(got, "  Restarted daemon") {
		t.Fatalf("expected restart message, got %q", got)
	}
}

// writeTestFile creates parent directories as needed so reset tests can seed files under a temporary data directory.
func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
