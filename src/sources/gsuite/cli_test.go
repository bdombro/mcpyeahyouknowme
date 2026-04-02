package gsuite

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
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

// Verifies info output defaults to a disabled status when the gsuite source has not been enabled.
func TestInfoLines_NotLoggedIn_CLI(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}
	if !containsStr(lines[0], "disabled") {
		t.Errorf("expected disabled status, got: %q", lines[0])
	}
}

// Verifies info output includes the cached account email when the source is enabled and authenticated.
func TestInfoLines_WithEmail(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	os.WriteFile(dir+"/gsuite_token.json", []byte(`{"access_token":"x"}`), 0600)
	os.WriteFile(dir+"/gsuite_email.txt", []byte("me@test.com"), 0600)

	lines := InfoLines(dir)
	found := false
	for _, l := range lines {
		if containsStr(l, "me@test.com") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected email in lines, got: %v", lines)
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
			if containsStr(l, "~") && containsStr(l, "MB") {
				foundAppSize = true
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

// Verifies disabled-source info output shows disabled status for the source and each app without legacy wording.
func TestInfoLines_DisabledSourceDisablesApps(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	disabledCount := 0
	for _, l := range lines {
		if containsStr(l, "disabled") {
			disabledCount++
		}
		if containsStr(l, "source disabled") {
			t.Fatalf("unexpected legacy disabled suffix in line %q", l)
		}
	}
	if disabledCount < len(allApps)+1 {
		t.Fatalf("expected disabled status plus disabled app lines, got %v", lines)
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
