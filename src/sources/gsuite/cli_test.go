package gsuite

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcpyeahyouknowme/core"

	_ "modernc.org/sqlite"
)

// Verifies login detection follows token-file presence without needing a live Google auth flow.
func TestIsLoggedIn(t *testing.T) {
	dir := t.TempDir()
	if IsLoggedIn(dir) {
		t.Fatal("expected false without token")
	}
	if err := os.WriteFile(filepath.Join(dir, "gsuite_token.json"), []byte(`{"access_token":"x"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if !IsLoggedIn(dir) {
		t.Fatal("expected true with token")
	}
}

// Verifies SessionAuthDisplay returns no without a token and signed in when only the token exists.
func TestSessionAuthDisplay_states(t *testing.T) {
	dir := t.TempDir()
	if got := SessionAuthDisplay(dir); got != "no" {
		t.Fatalf("no token: got %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "gsuite_token.json"), []byte(`{"access_token":"x"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if got := SessionAuthDisplay(dir); got != "signed in" {
		t.Fatalf("token without email: got %q", got)
	}
}

// Verifies info output defaults to nil when the gsuite source has not been enabled.
func TestInfoLines_NotLoggedIn_CLI(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) != 0 {
		t.Errorf("expected no lines when disabled, got: %v", lines)
	}
}

// Verifies SessionAuthDisplay returns the cached account email when a token and email file exist.
func TestSessionAuthDisplay_withEmail(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	os.WriteFile(dir+"/gsuite_token.json", []byte(`{"access_token":"x"}`), 0600)
	os.WriteFile(dir+"/gsuite_email.txt", []byte("me@test.com"), 0600)

	if got := SessionAuthDisplay(dir); got != "me@test.com" {
		t.Fatalf("SessionAuthDisplay = %q", got)
	}
	lines := InfoLines(dir)
	for _, l := range lines {
		if containsStr(l, "me@test.com") {
			t.Fatalf("email should not duplicate in InfoLines (status Auth line owns it), got line %q", l)
		}
	}
}

// Verifies info output shows a login hint when the source is enabled but no token exists.
func TestInfoLines_EnabledNotAuthenticated(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	lines := InfoLines(dir)
	if len(lines) != 1 || !containsStr(lines[0], "Hint") || !containsStr(lines[0], "login") {
		t.Errorf("expected login hint, got: %v", lines)
	}
}
// Verifies info output marks every app disabled when the source is enabled but no apps are selected.
func TestInfoLines_AllAppsDisabled(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	os.WriteFile(dir+"/gsuite_token.json", []byte(`{"access_token":"x"}`), 0600)

	src := &Source{dataDir: dir, apps: AppsConfig{}}
	// All apps disabled
	for _, app := range allApps {
		src.apps.SetEnabled(app.name, false)
	}
	if err := src.saveAppsConfig(src.apps); err != nil {
		t.Fatalf("saveAppsConfig: %v", err)
	}

	lines := InfoLines(dir)
	disabledCount := 0
	for _, l := range lines {
		if containsStr(l, "disabled") {
			disabledCount++
		}
	}
	if disabledCount != len(allApps) {
		t.Errorf("expected %d disabled lines, got %d\nlines: %v", len(allApps), disabledCount, lines)
	}
}

// Verifies info output includes DB size and per-app size/count details when synced data exists.
func TestInfoLines_WithDB(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	os.WriteFile(dir+"/gsuite_token.json", []byte(`{"access_token":"x"}`), 0600)

	src := &Source{dataDir: dir}
	apps := DefaultAppsConfig()
	apps.SetEnabled("docs", true)
	if err := src.saveAppsConfig(apps); err != nil {
		t.Fatalf("saveAppsConfig: %v", err)
	}

	// Create a real DB via openGSuiteDB and seed it
	db, err := openGSuiteDB(dir)
	if err != nil {
		t.Fatalf("openGSuiteDB: %v", err)
	}
	seedDocs(t, db)
	db.Close()

	lines := InfoLines(dir)
	// Should include count line for Docs and omit the "Google " prefix.
	found := false
	foundDBSize := false
	foundAppSize := false
	for _, l := range lines {
		if containsStr(l, "Docs") {
			found = true
			if containsStr(l, "MB") {
				foundAppSize = true
			}
			if containsStr(l, "~") {
				t.Fatalf("expected docs size without approximation marker, got: %v", lines)
			}
		}
		if containsStr(l, "Database:") && containsStr(l, "MB") {
			foundDBSize = true
		}
		if containsStr(l, "Google Docs") {
			t.Fatalf("expected shortened app name, got: %v", lines)
		}
	}
	if !found {
		t.Errorf("expected 'Docs' in info lines, got: %v", lines)
	}
	if !foundDBSize {
		t.Errorf("expected database size line in info lines, got: %v", lines)
	}
	if !foundAppSize {
		t.Errorf("expected app size suffix in docs line, got: %v", lines)
	}
}

// Verifies reset aborts cleanly when the interactive confirmation is declined.
func TestRunReset_Abort(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/gsuite_token.json", []byte(`{"access_token":"x"}`), 0600)

	// Redirect stdin to simulate "no"
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.WriteString("no\n")
	w.Close()
	defer func() { os.Stdin = oldStdin }()

	// Should not panic and should not delete the token
	RunReset(dir)

	if _, err := os.Stat(dir + "/gsuite_token.json"); os.IsNotExist(err) {
		t.Error("token should not be deleted when reset is aborted")
	}
}

// Verifies reset confirmation removes GSuite local files, disables the source, and clears stale GSuite rows from search.db.
func TestRunReset_confirmedClearsSearchRows(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"gsuite.db",
		"gsuite.db-wal",
		"gsuite.db-shm",
		"gsuite_token.json",
		"gsuite_email.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("seed"), 0644); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
	}
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	seedGSuiteSearchIndex(t, dir)

	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	if _, err := w.WriteString("yes\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	w.Close()
	defer func() { os.Stdin = oldStdin }()

	RunReset(dir)

	for _, rel := range []string{
		"gsuite.db",
		"gsuite.db-wal",
		"gsuite.db-shm",
		"gsuite_token.json",
		"gsuite_email.txt",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err = %v", rel, err)
		}
	}
	if core.LoadConfig(dir).Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite to be disabled after reset")
	}
	assertGSuiteSearchSourceCount(t, dir, "gsuite", 0)
	assertGSuiteSearchSourceCount(t, dir, "notebook", 1)
}

// seedGSuiteSearchIndex creates a minimal shared search index so the reset test can verify only GSuite rows are cleared.
func seedGSuiteSearchIndex(t *testing.T, dataDir string) {
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
			('gsuite', 'thread-1', 'email_thread_subject', 'John Thomas', 'John Thomas has 3 kids'),
			('notebook', 'note-1', 'note_title', 'John Thomas', 'John Thomas');
	`); err != nil {
		t.Fatalf("seed search db: %v", err)
	}
}

// assertGSuiteSearchSourceCount checks the remaining row count for one source after GSuite reset mutates search.db.
func assertGSuiteSearchSourceCount(t *testing.T, dataDir, source string, want int) {
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

// Verifies interactive app selection only enables all apps when the caller explicitly chooses the `all` path.
func TestPromptAppSelection_AllOption(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantAllApps bool
	}{
		{name: "zero keeps none", input: "0\n", wantAllApps: false},
		{name: "all enables all", input: "all\n", wantAllApps: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldStdin := os.Stdin
			oldStdout := os.Stdout
			inR, inW, err := os.Pipe()
			if err != nil {
				t.Fatalf("stdin pipe: %v", err)
			}
			outR, outW, err := os.Pipe()
			if err != nil {
				t.Fatalf("stdout pipe: %v", err)
			}
			os.Stdin = inR
			os.Stdout = outW
			defer func() {
				os.Stdin = oldStdin
				os.Stdout = oldStdout
			}()

			if _, err := inW.WriteString(tc.input); err != nil {
				t.Fatalf("write input: %v", err)
			}
			inW.Close()

			apps := promptAppSelection()

			outW.Close()
			if _, err := io.ReadAll(outR); err != nil {
				t.Fatalf("read stdout: %v", err)
			}

			for _, app := range allApps {
				if apps.IsEnabled(app.name) != tc.wantAllApps {
					t.Fatalf("expected %s enabled=%v", app.name, tc.wantAllApps)
				}
			}
		})
	}
}

// Verifies the interactive apps command persists an `all` selection back into the source config.
func TestRunApps_AllEnablesEverything(t *testing.T) {
	dir := t.TempDir()

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	os.Stdin = inR
	os.Stdout = outW
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	if _, err := inW.WriteString("all\n"); err != nil {
		t.Fatalf("write input: %v", err)
	}
	inW.Close()

	RunApps(dir)

	outW.Close()
	if _, err := io.ReadAll(outR); err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	src := NewSource(dir)
	defer src.Close()
	for _, app := range allApps {
		if !src.apps.IsEnabled(app.name) {
			t.Fatalf("expected %s to be enabled", app.name)
		}
	}
}

// Verifies disabled-source info output returns no lines (Config: line is owned by status renderer).
func TestInfoLines_DisabledSourceDisablesApps(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) != 0 {
		t.Fatalf("expected no lines when disabled, got %v", lines)
	}
}

// Verifies sync-status formatting preserves the expected syncing wording across supported transient status shapes.
func TestFormatSyncStatus_Variants(t *testing.T) {
	tests := []struct {
		status     string
		wantSubstr string
	}{
		{"syncing:42", "syncing"},
		{"syncing", "syncing"},
	}
	for _, tc := range tests {
		got := formatSyncStatus(tc.status, zeroTimeVal, 42)
		if !containsStr(got, tc.wantSubstr) {
			t.Errorf("formatSyncStatus(%q) = %q, want substr %q", tc.status, got, tc.wantSubstr)
		}
	}
}

// zeroTimeVal is a zero-value time.Time for use in tests.
var zeroTimeVal = time.Time{}

// Verifies RunEnable with "all" enables every app and sets source enabled.
func TestRunEnable_all(t *testing.T) {
	dir := t.TempDir()
	RunEnable(dir, []string{"all"})
	cfg := core.LoadConfig(dir)
	if !cfg.Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite source enabled")
	}
	src := NewSource(dir)
	defer src.Close()
	for _, app := range allApps {
		if !src.apps.IsEnabled(app.name) {
			t.Errorf("expected %s enabled", app.name)
		}
	}
}

// Verifies RunEnable with a specific app name enables only that app and sets source enabled.
func TestRunEnable_singleApp(t *testing.T) {
	dir := t.TempDir()
	RunEnable(dir, []string{"docs"})
	cfg := core.LoadConfig(dir)
	if !cfg.Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite source enabled")
	}
	src := NewSource(dir)
	defer src.Close()
	if !src.apps.IsEnabled("docs") {
		t.Fatal("expected docs enabled")
	}
	if src.apps.IsEnabled("gmail") {
		t.Fatal("expected gmail still disabled")
	}
}

// Verifies RunEnable with no args enables all apps and sets source enabled.
func TestRunEnable_noArgs(t *testing.T) {
	dir := t.TempDir()
	RunEnable(dir, nil)
	cfg := core.LoadConfig(dir)
	if !cfg.Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite source enabled")
	}
}

// Verifies RunDisable with "all" disables the source.
func TestRunDisable_all(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatal(err)
	}
	RunDisable(dir, []string{"all"})
	cfg := core.LoadConfig(dir)
	if cfg.Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite source disabled")
	}
}

// Verifies RunDisable with a specific app name disables only that app.
func TestRunDisable_singleApp(t *testing.T) {
	dir := t.TempDir()
	src := NewSource(dir)
	defer src.Close()
	src.apps.SetEnabled("docs", true)
	src.apps.SetEnabled("gmail", true)
	if err := src.saveAppsConfig(src.apps); err != nil {
		t.Fatal(err)
	}

	RunDisable(dir, []string{"docs"})

	src2 := NewSource(dir)
	defer src2.Close()
	if src2.apps.IsEnabled("docs") {
		t.Fatal("expected docs disabled")
	}
	if !src2.apps.IsEnabled("gmail") {
		t.Fatal("expected gmail still enabled")
	}
}

// Verifies RunDisable with no args disables the source without touching app config.
func TestRunDisable_noArgs(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatal(err)
	}
	RunDisable(dir, nil)
	if core.LoadConfig(dir).Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite source disabled")
	}
}

// Verifies isValidAppName accepts known app names and rejects unknowns.
func TestIsValidAppName(t *testing.T) {
	for _, app := range allApps {
		if !isValidAppName(app.name) {
			t.Errorf("expected %q to be valid", app.name)
		}
	}
	if isValidAppName("bogus") {
		t.Error("expected bogus to be invalid")
	}
}
