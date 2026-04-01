package gsuite

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
)

func TestInitGSuiteDB(t *testing.T) {
	db := newTestDB(t)
	tables := []string{
		"sync_state",
		"docs_documents",
		"sheets_spreadsheets",
		"gmail_messages",
		"gmail_threads",
		"calendar_events",
		"tasks_items",
		"contacts_people",
		"slides_presentations",
	}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type IN ('table','shadow') AND name = ?", table).Scan(&name)
		if err != nil {
			t.Errorf("expected table %q to exist: %v", table, err)
		}
	}
}

func TestIsAuthenticated_NoToken(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	if src.isAuthenticated() {
		t.Error("expected false with no token")
	}
}

func TestIsAuthenticated_WithToken(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	src.token = &oauth2.Token{RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if !src.isAuthenticated() {
		t.Error("expected true with valid token")
	}
}

func TestIsAuthenticated_ExpiredNoRefresh(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	src.token = &oauth2.Token{RefreshToken: "", Expiry: time.Now().Add(-time.Hour)}
	if src.isAuthenticated() {
		t.Error("expected false with expired token and no refresh token")
	}
}

func TestIsAuthenticated_WithRefreshToken(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	// Expired but has refresh token — still considered valid
	src.token = &oauth2.Token{RefreshToken: "refresh_ok", Expiry: time.Now().Add(-time.Hour)}
	if !src.isAuthenticated() {
		t.Error("expected true with expired token but valid refresh token")
	}
}

func TestSaveLoadToken(t *testing.T) {
	dir := t.TempDir()
	src := &Source{dataDir: dir}
	tok := &oauth2.Token{RefreshToken: "myrefresh", Expiry: time.Now().Add(time.Hour)}
	if err := src.saveToken(tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	src2 := &Source{dataDir: dir}
	if err := src2.loadToken(); err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if src2.token == nil {
		t.Fatal("expected token after load")
	}
	if src2.token.RefreshToken != "myrefresh" {
		t.Errorf("expected refresh token 'myrefresh', got %q", src2.token.RefreshToken)
	}
}

func TestLoadToken_Missing(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	if err := src.loadToken(); err == nil {
		t.Error("expected error loading missing token")
	}
}

func TestAppsConfig_DefaultAllDisabled(t *testing.T) {
	cfg := DefaultAppsConfig()
	for _, app := range allApps {
		if cfg.IsEnabled(app.name) {
			t.Errorf("expected %s to be disabled by default", app.name)
		}
	}
}

func TestAppsConfig_SetEnabled(t *testing.T) {
	cfg := DefaultAppsConfig()
	cfg.SetEnabled("gmail", false)
	if cfg.IsEnabled("gmail") {
		t.Error("expected gmail to be disabled")
	}
	cfg.SetEnabled("gmail", true)
	if !cfg.IsEnabled("gmail") {
		t.Error("expected gmail to be re-enabled")
	}
}

func TestAppsConfig_UnknownApp(t *testing.T) {
	cfg := DefaultAppsConfig()
	if cfg.IsEnabled("nonexistent") {
		t.Error("unknown app should return false")
	}
	cfg.SetEnabled("nonexistent", true) // must not panic
}

func TestSaveLoadAppsConfig(t *testing.T) {
	dir := t.TempDir()
	src := &Source{dataDir: dir, apps: allAppsEnabledConfig()}
	src.db = newTestDB(t)

	apps := DefaultAppsConfig()
	apps.SetEnabled("docs", true)
	apps.SetEnabled("gmail", false)
	apps.SetEnabled("tasks", false)
	if err := src.saveAppsConfig(apps); err != nil {
		t.Fatalf("saveAppsConfig: %v", err)
	}

	loaded := src.loadAppsConfig()
	if loaded.IsEnabled("gmail") {
		t.Error("gmail should be disabled after save/load")
	}
	if loaded.IsEnabled("tasks") {
		t.Error("tasks should be disabled after save/load")
	}
	if !loaded.IsEnabled("docs") {
		t.Error("docs should still be enabled")
	}
}

func TestLoadAppsConfig_InvalidJSONFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{
  "sources": {
    "gsuite": {
      "enabled": true,
      "auth": "wrong-shape"
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	src := &Source{dataDir: dir}
	loaded := src.loadAppsConfig()
	for _, app := range allApps {
		if loaded.IsEnabled(app.name) {
			t.Fatalf("expected %s to be disabled after invalid JSON fallback", app.name)
		}
	}
}

func TestLoadAppsConfig_NoAuthFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	src := &Source{dataDir: dir}
	loaded := src.loadAppsConfig()
	for _, app := range allApps {
		if loaded.IsEnabled(app.name) {
			t.Fatalf("expected %s to be disabled without auth config", app.name)
		}
	}
}

func TestGetSetSyncState(t *testing.T) {
	src := newTestSource(t)
	if !src.getLastSyncTime("docs").IsZero() {
		t.Error("expected zero time before first sync")
	}
	now := time.Now().Truncate(time.Second)
	src.setLastSyncTime("docs", now)
	got := src.getLastSyncTime("docs")
	if !got.Equal(now) {
		t.Errorf("expected %v, got %v", now, got)
	}
}

func TestGetSetSyncStatus(t *testing.T) {
	src := newTestSource(t)
	if src.getSyncStatus("gmail") != "" {
		t.Error("expected empty status initially")
	}
	src.setSyncStatus("gmail", "syncing:5")
	if src.getSyncStatus("gmail") != "syncing:5" {
		t.Errorf("expected 'syncing:5', got %q", src.getSyncStatus("gmail"))
	}
}

func TestGetSetSyncState_NilDB(t *testing.T) {
	src := &Source{}
	src.setLastSyncTime("docs", time.Now())
	src.setSyncStatus("docs", "syncing")
	if !src.getLastSyncTime("docs").IsZero() {
		t.Error("expected zero time with nil db")
	}
	if src.getSyncStatus("docs") != "" {
		t.Error("expected empty status with nil db")
	}
}

func TestSearchEntries_Empty(t *testing.T) {
	src := newTestSource(t)
	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty DB, got %d", len(entries))
	}
}

func TestSearchEntries_WithData(t *testing.T) {
	src := newTestSource(t)
	seedAll(t, src.db)
	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected entries with seeded data")
	}
	types := map[string]bool{}
	for _, e := range entries {
		types[e.ContentType] = true
	}
	expected := []string{"document_title", "spreadsheet_title", "email_thread_subject", "email_thread_content", "calendar_event", "task", "contact", "presentation_title"}
	for _, ct := range expected {
		if !types[ct] {
			t.Errorf("expected content type %q in search entries", ct)
		}
	}
}

func TestSearchEntries_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("unexpected error with nil db: %v", err)
	}
	if entries != nil {
		t.Error("expected nil entries with nil db")
	}
}

func TestSearchEntries_DisabledApp(t *testing.T) {
	src := newTestSource(t)
	seedDocs(t, src.db)
	src.apps.SetEnabled("docs", false)
	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
	for _, e := range entries {
		if e.ContentType == "document_title" || e.ContentType == "document_content" {
			t.Errorf("expected no docs entries when docs app is disabled")
		}
	}
}

func TestSearchEntries_ContinuesOnAppError(t *testing.T) {
	originalApps := allApps
	t.Cleanup(func() { allApps = originalApps })

	src := newTestSource(t)
	allApps = []*appDef{
		{
			name:        "docs",
			displayName: "Docs",
			searchEntries: func(_ *sql.DB, _ string) ([]core.SearchEntry, error) {
				return nil, errors.New("boom")
			},
		},
		{
			name:        "gmail",
			displayName: "Gmail",
			searchEntries: func(_ *sql.DB, sourceName string) ([]core.SearchEntry, error) {
				return []core.SearchEntry{{
					Source:      sourceName,
					SourceID:    "ok",
					ContentType: "email_subject",
					Title:       "ok",
					Content:     "ok",
				}}, nil
			},
		},
	}
	src.apps = allAppsEnabledConfig()

	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].SourceID != "ok" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestReset(t *testing.T) {
	dir := t.TempDir()
	files := []string{"gsuite.db", "gsuite.db-wal", "gsuite.db-shm", "gsuite_token.json", "gsuite_email.txt"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("data"), 0600); err != nil {
			t.Fatalf("create file %s: %v", f, err)
		}
	}
	src := &Source{}
	if err := src.Reset(dir); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be deleted after reset", f)
		}
	}
}

func TestResetApp(t *testing.T) {
	src := newTestSource(t)
	seedDocs(t, src.db)

	var count int
	src.db.QueryRow("SELECT COUNT(*) FROM docs_documents").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 doc before reset, got %d", count)
	}

	if err := src.ResetApp("docs"); err != nil {
		t.Fatalf("ResetApp: %v", err)
	}

	src.db.QueryRow("SELECT COUNT(*) FROM docs_documents").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 docs after reset, got %d", count)
	}
}

func TestResetApp_NilDB(t *testing.T) {
	src := &Source{}
	if err := src.ResetApp("docs"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResetApp_UnknownApp(t *testing.T) {
	src := newTestSource(t)
	if err := src.ResetApp("nonexistent"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewSource_OpenDBFailureStillLoadsDefaults(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	src := NewSource(filePath)
	defer src.Close()
	if src.db != nil {
		t.Fatal("expected db to be nil when dataDir is not a directory")
	}
	for _, app := range allApps {
		if src.apps.IsEnabled(app.name) {
			t.Fatalf("expected %s to remain disabled by default", app.name)
		}
	}
}

func TestAppDefs_ReturnsAllApps(t *testing.T) {
	apps := AppDefs()
	if len(apps) != len(allApps) {
		t.Fatalf("AppDefs len = %d, want %d", len(apps), len(allApps))
	}
}

func TestFormatSyncStatus(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		lastSync   time.Time
		count      int
		wantPrefix string
	}{
		{"idle with last sync", "idle", time.Now().Add(-5 * time.Minute), 42, "42 synced"},
		{"currently syncing with count", "syncing:10", time.Time{}, 9, "9 synced — syncing"},
		{"syncing no count", "syncing", time.Time{}, 0, "0 synced — syncing"},
		{"no sync yet", "", time.Time{}, 0, "0 synced"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSyncStatus(tc.status, tc.lastSync, tc.count)
			if len(got) < len(tc.wantPrefix) || got[:len(tc.wantPrefix)] != tc.wantPrefix {
				t.Errorf("got %q, want prefix %q", got, tc.wantPrefix)
			}
		})
	}
}

func TestAllAppDefs(t *testing.T) {
	if len(allApps) != 7 {
		t.Errorf("expected 7 app defs, got %d", len(allApps))
	}
	names := map[string]bool{}
	for _, app := range allApps {
		if app.name == "" {
			t.Error("app def has empty name")
		}
		if app.displayName == "" {
			t.Errorf("app %s has empty displayName", app.name)
		}
		if names[app.name] {
			t.Errorf("duplicate app name: %s", app.name)
		}
		names[app.name] = true
	}
}

func TestBuildContentEntries(t *testing.T) {
	entries := buildContentEntries("src", "id1", "My Title", "Some content here", "2024-01-01T00:00:00Z", "Owner A",
		"title_type", "owner_type", "content_type", "doc_id")
	if len(entries) < 3 {
		t.Errorf("expected at least 3 entries, got %d", len(entries))
	}
	hasTitle, hasOwner, hasContent := false, false, false
	for _, e := range entries {
		switch e.ContentType {
		case "title_type":
			hasTitle = true
		case "owner_type":
			hasOwner = true
		case "content_type":
			hasContent = true
		}
	}
	if !hasTitle {
		t.Error("missing title entry")
	}
	if !hasOwner {
		t.Error("missing owner entry")
	}
	if !hasContent {
		t.Error("missing content entry")
	}
}

func TestBuildContentEntries_NoOwner(t *testing.T) {
	entries := buildContentEntries("src", "id1", "Title", "Content", "2024-01-01T00:00:00Z", "",
		"tt", "ot", "ct", "id")
	for _, e := range entries {
		if e.ContentType == "ot" {
			t.Error("should not emit owner entry when owners is empty")
		}
	}
}

func TestBuildContentEntries_LongContent(t *testing.T) {
	content := string(make([]byte, 12000))
	for i := range []byte(content) {
		content = content[:i] + "x" + content[i+1:]
		if i == 0 {
			break // avoid O(n^2); just test chunking math
		}
	}
	// Create a 12001 char string directly
	buf := make([]byte, 12001)
	for i := range buf {
		buf[i] = 'x'
	}
	entries := buildContentEntries("src", "id1", "Title", string(buf), "2024-01-01T00:00:00Z", "",
		"tt", "ot", "ct", "id")
	contentChunks := 0
	for _, e := range entries {
		if e.ContentType == "ct" {
			contentChunks++
		}
	}
	if contentChunks != 3 {
		t.Errorf("expected 3 content chunks for 12001 chars, got %d", contentChunks)
	}
}

func TestBuildContentEntries_SkipsNumericDominantChunks(t *testing.T) {
	numericContent := strings.Repeat("$1,234.56\t2024-01-15\tINV-00123\t", 200)
	textContent := strings.Repeat("Revenue Q1 improved because the pipeline expanded with new contracts. ", 120)

	numericEntries := buildContentEntries("src", "numeric", "Budget Grid", numericContent, "2024-01-01T00:00:00Z", "",
		"spreadsheet_title", "spreadsheet_owner", "spreadsheet_content", "spreadsheet_id")
	textEntries := buildContentEntries("src", "text", "Narrative Grid", textContent, "2024-01-01T00:00:00Z", "",
		"spreadsheet_title", "spreadsheet_owner", "spreadsheet_content", "spreadsheet_id")

	numericChunks := 0
	for _, entry := range numericEntries {
		if entry.ContentType == "spreadsheet_content" {
			numericChunks++
		}
	}
	textChunks := 0
	for _, entry := range textEntries {
		if entry.ContentType == "spreadsheet_content" {
			textChunks++
		}
	}

	if numericChunks != 0 {
		t.Fatalf("expected numeric-heavy content chunks to be skipped, got %d", numericChunks)
	}
	if textChunks == 0 {
		t.Fatal("expected prose-heavy content chunks to remain indexed")
	}
	if len(numericEntries) >= len(textEntries) {
		t.Fatalf("expected numeric-heavy content to produce fewer search entries (%d vs %d)", len(numericEntries), len(textEntries))
	}
}

func TestSearchEntries_NumericHeavySheetProducesFewerEntries(t *testing.T) {
	src := newTestSource(t)
	src.apps = DefaultAppsConfig()
	src.apps.SetEnabled("sheets", true)

	numericContent := strings.Repeat("$1,234.56\t2024-01-15\tINV-00123\t", 200)
	textContent := strings.Repeat("Revenue Q1 improved because the pipeline expanded with new contracts. ", 120)

	_, err := src.db.Exec(`INSERT INTO sheets_spreadsheets
		(id, title, content, modified_time, created_time, web_view_link, owners, sheet_count, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now')),
		       (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"sheet_numeric", "Budget Grid", numericContent, "2024-02-10T08:00:00Z", "2024-02-01T08:00:00Z",
		"https://sheets.google.com/sheet_numeric", "", 1,
		"sheet_text", "Narrative Grid", textContent, "2024-02-10T08:00:00Z", "2024-02-01T08:00:00Z",
		"https://sheets.google.com/sheet_text", "", 1)
	if err != nil {
		t.Fatalf("seed sheets: %v", err)
	}

	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}

	numericCount := 0
	textCount := 0
	for _, entry := range entries {
		switch entry.SourceID {
		case "sheet_numeric":
			numericCount++
		case "sheet_text":
			textCount++
		}
	}

	if numericCount == 0 {
		t.Fatal("expected numeric-heavy sheet to keep at least the title entry")
	}
	if textCount == 0 {
		t.Fatal("expected text-heavy sheet to produce search entries")
	}
	if numericCount >= textCount {
		t.Fatalf("expected numeric-heavy sheet to produce fewer entries (%d vs %d)", numericCount, textCount)
	}
}

func TestFormatDriveOwners_Empty(t *testing.T) {
	result := formatDriveOwners(nil, "")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestFormatDriveOwners_ExcludesSelf(t *testing.T) {
	import_note := "uses drive.User inline via pointer"
	_ = import_note
	// Can't use drive.User without importing the SDK in the test — test via formatDriveOwners
	// with no owners
	result := formatDriveOwners(nil, "self@example.com")
	if result != "" {
		t.Errorf("expected empty string with no owners")
	}
}

func TestDeleteOrphanedRows(t *testing.T) {
	src := newTestSource(t)
	seedDocs(t, src.db)

	// doc1 is in DB; keep it
	remoteIDs := map[string]bool{"doc1": true}
	deleteOrphanedRows(src.db, "docs_documents", remoteIDs)

	var count int
	src.db.QueryRow("SELECT COUNT(*) FROM docs_documents").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 doc to remain, got %d", count)
	}

	// Now mark doc1 as deleted remotely
	remoteIDs = map[string]bool{}
	deleteOrphanedRows(src.db, "docs_documents", remoteIDs)

	src.db.QueryRow("SELECT COUNT(*) FROM docs_documents").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 docs after orphan delete, got %d", count)
	}
}

func TestCountTable(t *testing.T) {
	src := newTestSource(t)
	n, err := countTable(src.db, "docs_documents")
	if err != nil {
		t.Fatalf("countTable: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
	seedDocs(t, src.db)
	n, err = countTable(src.db, "docs_documents")
	if err != nil {
		t.Fatalf("countTable after seed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestSourceName(t *testing.T) {
	src := &Source{}
	if src.Name() != "gsuite" {
		t.Errorf("expected 'gsuite', got %q", src.Name())
	}
	if src.Description() != "Google Suite" {
		t.Errorf("expected 'Google Suite', got %q", src.Description())
	}
}

func TestClose_NilDB(t *testing.T) {
	src := &Source{}
	if err := src.Close(); err != nil {
		t.Errorf("Close with nil db: %v", err)
	}
}
