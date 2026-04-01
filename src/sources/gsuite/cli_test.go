package gsuite

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
)

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

func TestInfoLines_WithDB(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	os.WriteFile(dir+"/gsuite_token.json", []byte(`{"access_token":"x"}`), 0600)

	// Create a real DB via openGSuiteDB and seed it
	db, err := openGSuiteDB(dir)
	if err != nil {
		t.Fatalf("openGSuiteDB: %v", err)
	}
	seedDocs(t, db)
	db.Close()

	lines := InfoLines(dir)
	// Should include count line for docs
	found := false
	for _, l := range lines {
		if containsStr(l, "Google Docs") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Google Docs' in info lines, got: %v", lines)
	}
}

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

func TestInfoLines_DisabledSourceDisablesApps(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	disabledCount := 0
	for _, l := range lines {
		if containsStr(l, "disabled") {
			disabledCount++
		}
	}
	if disabledCount < len(allApps)+1 {
		t.Fatalf("expected disabled status plus disabled app lines, got %v", lines)
	}
}

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
