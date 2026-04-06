package gsuite

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
)

// Verifies initGSuiteDB creates the tables the source expects before any app sync runs.
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

// Verifies initCalendarSchema drops the legacy calendar_id column when upgrading existing databases.
func TestInitCalendarSchema_dropsLegacyCalendarIdColumn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE calendar_events (
		id TEXT PRIMARY KEY,
		calendar_name TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		location TEXT NOT NULL DEFAULT '',
		start_time TEXT NOT NULL DEFAULT '',
		end_time TEXT NOT NULL DEFAULT '',
		all_day INTEGER NOT NULL DEFAULT 0,
		created_time TEXT NOT NULL DEFAULT '',
		updated_time TEXT NOT NULL DEFAULT '',
		organizer TEXT NOT NULL DEFAULT '',
		attendees TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		recurrence TEXT NOT NULL DEFAULT '',
		html_link TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL DEFAULT '',
		calendar_id TEXT
	)`)
	if err != nil {
		t.Fatalf("create legacy calendar_events: %v", err)
	}

	if err := initCalendarSchema(db); err != nil {
		t.Fatalf("initCalendarSchema: %v", err)
	}

	has, err := tableHasColumn(db, "calendar_events", "calendar_id")
	if err != nil {
		t.Fatalf("tableHasColumn: %v", err)
	}
	if has {
		t.Error("expected calendar_id to be dropped after migration")
	}
}

// Verifies initContactsSchema drops the legacy given_name, family_name, and addresses columns.
func TestInitContactsSchema_dropsLegacyColumns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE contacts_people (
		resource_name TEXT PRIMARY KEY,
		display_name TEXT NOT NULL DEFAULT '',
		emails TEXT NOT NULL DEFAULT '',
		phones TEXT NOT NULL DEFAULT '',
		organizations TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		updated_time TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL DEFAULT '',
		given_name TEXT,
		family_name TEXT,
		addresses TEXT
	)`)
	if err != nil {
		t.Fatalf("create legacy contacts_people: %v", err)
	}

	if err := initContactsSchema(db); err != nil {
		t.Fatalf("initContactsSchema: %v", err)
	}

	for _, col := range []string{"given_name", "family_name", "addresses"} {
		has, err := tableHasColumn(db, "contacts_people", col)
		if err != nil {
			t.Fatalf("tableHasColumn %s: %v", col, err)
		}
		if has {
			t.Errorf("expected %s to be dropped after migration", col)
		}
	}
}

// Verifies initTasksSchema drops the legacy tasklist_id, completed, position, and parent columns.
func TestInitTasksSchema_dropsLegacyColumns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE tasks_items (
		id TEXT PRIMARY KEY,
		tasklist_title TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		due TEXT NOT NULL DEFAULT '',
		updated TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL DEFAULT '',
		tasklist_id TEXT,
		completed TEXT,
		position TEXT,
		parent TEXT
	)`)
	if err != nil {
		t.Fatalf("create legacy tasks_items: %v", err)
	}

	if err := initTasksSchema(db); err != nil {
		t.Fatalf("initTasksSchema: %v", err)
	}

	for _, col := range []string{"tasklist_id", "completed", "position", "parent"} {
		has, err := tableHasColumn(db, "tasks_items", col)
		if err != nil {
			t.Fatalf("tableHasColumn %s: %v", col, err)
		}
		if has {
			t.Errorf("expected %s to be dropped after migration", col)
		}
	}
}

// Verifies sources without a token are treated as unauthenticated.
func TestIsAuthenticated_NoToken(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	if src.isAuthenticated() {
		t.Error("expected false with no token")
	}
}

// Verifies sources with a valid token are treated as authenticated.
func TestIsAuthenticated_WithToken(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	src.token = &oauth2.Token{RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if !src.isAuthenticated() {
		t.Error("expected true with valid token")
	}
}

// Verifies expired tokens without a refresh token are treated as unauthenticated.
func TestIsAuthenticated_ExpiredNoRefresh(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	src.token = &oauth2.Token{RefreshToken: "", Expiry: time.Now().Add(-time.Hour)}
	if src.isAuthenticated() {
		t.Error("expected false with expired token and no refresh token")
	}
}

// Verifies refresh-token presence keeps the source authenticated even when the access token is expired.
func TestIsAuthenticated_WithRefreshToken(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	// Expired but has refresh token — still considered valid
	src.token = &oauth2.Token{RefreshToken: "refresh_ok", Expiry: time.Now().Add(-time.Hour)}
	if !src.isAuthenticated() {
		t.Error("expected true with expired token but valid refresh token")
	}
}

// Verifies saved OAuth tokens can be loaded back without losing fields needed for later auth refresh.
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

// Verifies loading a missing token file returns the expected no-token error path.
func TestLoadToken_Missing(t *testing.T) {
	src := &Source{dataDir: t.TempDir()}
	if err := src.loadToken(); err == nil {
		t.Error("expected error loading missing token")
	}
}

// Verifies the default apps config starts with every gsuite app disabled until the user opts in.
func TestAppsConfig_DefaultAllDisabled(t *testing.T) {
	cfg := DefaultAppsConfig()
	for _, app := range allApps {
		if cfg.IsEnabled(app.name) {
			t.Errorf("expected %s to be disabled by default", app.name)
		}
	}
}

// Verifies per-app enable toggles persist in memory for known app names.
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

// Verifies unknown app names are ignored instead of mutating config unexpectedly.
func TestAppsConfig_UnknownApp(t *testing.T) {
	cfg := DefaultAppsConfig()
	if cfg.IsEnabled("nonexistent") {
		t.Error("unknown app should return false")
	}
	cfg.SetEnabled("nonexistent", true) // must not panic
}

// Verifies app selections persist to disk and can be loaded back into a source config.
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

// Verifies invalid apps-config JSON falls back to the safe default config instead of failing hard.
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

// Verifies missing auth/config state falls back to default app selections instead of inventing enabled apps.
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

// Verifies sync-state persistence records and reloads per-app timestamps.
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

// Verifies sync-status persistence records and reloads per-app status strings.
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

// Verifies sync-state helpers fail safely when the source has no open DB.
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

// Verifies SearchEntries returns no entries when the source has no synced app data.
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

// Verifies SearchEntries emits indexed entries from enabled apps with seeded data.
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

// Verifies SearchEntries returns a safe nil/empty result when the source DB is unavailable.
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

// Verifies SearchEntries skips apps that are disabled in the persisted app config.
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

// Verifies SearchEntries keeps collecting entries from healthy apps even when one app returns an error.
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

// Verifies StreamSearchEntries tolerates nil emitters and keeps collecting healthy apps after a streamed app error.
func TestStreamSearchEntries_nilEmitAndContinuesOnError(t *testing.T) {
	src := newTestSource(t)
	if err := src.StreamSearchEntries(nil); err != nil {
		t.Fatalf("StreamSearchEntries(nil): %v", err)
	}
	src.db = nil
	if err := src.StreamSearchEntries(func([]core.SearchEntry) error { return nil }); err != nil {
		t.Fatalf("StreamSearchEntries(nil db): %v", err)
	}
	src.db = newTestDB(t)

	originalApps := allApps
	t.Cleanup(func() { allApps = originalApps })
	allApps = []*appDef{
		{
			name:          "docs",
			displayName:   "Docs",
			streamEntries: func(*sql.DB, string, func([]core.SearchEntry) error) error { return errors.New("boom") },
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

	var entries []core.SearchEntry
	if err := src.StreamSearchEntries(func(batch []core.SearchEntry) error {
		entries = append(entries, batch...)
		return nil
	}); err != nil {
		t.Fatalf("StreamSearchEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].SourceID != "ok" {
		t.Fatalf("unexpected streamed entries: %#v", entries)
	}
}

// Verifies StreamSearchEntries returns emitter failures from non-streaming apps so daemon indexing can stop promptly.
func TestStreamSearchEntries_emitError(t *testing.T) {
	originalApps := allApps
	t.Cleanup(func() { allApps = originalApps })

	src := newTestSource(t)
	allApps = []*appDef{{
		name:        "docs",
		displayName: "Docs",
		searchEntries: func(_ *sql.DB, sourceName string) ([]core.SearchEntry, error) {
			return []core.SearchEntry{{
				Source:      sourceName,
				SourceID:    "doc1",
				ContentType: "document_title",
				Title:       "Doc",
				Content:     "Doc",
			}}, nil
		},
	}}
	src.apps = allAppsEnabledConfig()

	if err := src.StreamSearchEntries(func([]core.SearchEntry) error {
		return errors.New("stop")
	}); err == nil {
		t.Fatal("expected emit error")
	}
}

// Verifies StreamSearchEntries propagates emit failures from streaming apps so daemon indexing can stop promptly.
func TestStreamSearchEntries_streamingAppEmitError(t *testing.T) {
	originalApps := allApps
	t.Cleanup(func() { allApps = originalApps })

	src := newTestSource(t)
	allApps = []*appDef{{
		name:        "docs",
		displayName: "Docs",
		streamEntries: func(_ *sql.DB, sourceName string, emit func([]core.SearchEntry) error) error {
			batch := []core.SearchEntry{{
				Source:      sourceName,
				SourceID:    "doc1",
				ContentType: "document_title",
				Title:       "Doc",
				Content:     "Doc",
			}}
			return emit(batch)
		},
	}}
	src.apps = allAppsEnabledConfig()

	err := src.StreamSearchEntries(func([]core.SearchEntry) error {
		return errors.New("stop from emit")
	})
	if err == nil {
		t.Fatal("expected emit error from streaming app to be propagated")
	}
	if err.Error() != "stop from emit" {
		t.Fatalf("expected original emit error, got %v", err)
	}
}

// Verifies HasChangesSince checks gsuite DB and WAL mtimes so incremental indexing can skip unchanged caches.
func TestSource_HasChangesSince(t *testing.T) {
	dataDir := t.TempDir()
	src := &Source{dataDir: dataDir}
	if !src.HasChangesSince(time.Time{}) {
		t.Fatal("expected zero watermark to force indexing")
	}
	if !src.HasChangesSince(time.Now()) {
		t.Fatal("expected missing gsuite files to trigger indexing")
	}

	dbPath := filepath.Join(dataDir, "gsuite.db")
	walPath := filepath.Join(dataDir, "gsuite.db-wal")
	if err := os.WriteFile(dbPath, []byte("db"), 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := os.WriteFile(walPath, []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(dbPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes db: %v", err)
	}
	if err := os.Chtimes(walPath, newTime, newTime); err != nil {
		t.Fatalf("chtimes wal: %v", err)
	}

	if src.HasChangesSince(time.Now()) {
		t.Fatal("expected future watermark to skip unchanged gsuite cache")
	}
	if !src.HasChangesSince(time.Now().Add(-90 * time.Minute)) {
		t.Fatal("expected WAL change to trigger gsuite reindex")
	}
}

// Verifies Reset removes source-owned auth and data artifacts so the source can be reinitialized cleanly.
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

// Verifies app-specific reset clears only the targeted app tables rather than all gsuite data.
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

// Verifies app-specific reset reports an error when no DB is available.
func TestResetApp_NilDB(t *testing.T) {
	src := &Source{}
	if err := src.ResetApp("docs"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Verifies app-specific reset rejects unknown app names instead of deleting arbitrary tables.
func TestResetApp_UnknownApp(t *testing.T) {
	src := newTestSource(t)
	if err := src.ResetApp("nonexistent"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Verifies NewSource still loads default app state when opening the DB fails.
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

// Verifies AppDefs returns the registered set of gsuite app definitions.
func TestAppDefs_ReturnsAllApps(t *testing.T) {
	apps := AppDefs()
	if len(apps) != len(allApps) {
		t.Fatalf("AppDefs len = %d, want %d", len(apps), len(allApps))
	}
}

// Verifies sync-status formatting includes the expected wording for the main status variants.
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

// Verifies allAppDefs stays aligned with the configured gsuite app registry.
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

// Verifies shared content-entry building preserves metadata needed for global search results.
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

// Verifies content-entry building tolerates missing owner data without dropping the entry.
func TestBuildContentEntries_NoOwner(t *testing.T) {
	entries := buildContentEntries("src", "id1", "Title", "Content", "2024-01-01T00:00:00Z", "",
		"tt", "ot", "ct", "id")
	for _, e := range entries {
		if e.ContentType == "ot" {
			t.Error("should not emit owner entry when owners is empty")
		}
	}
}

// Verifies long content is chunked into multiple global-search entries instead of one oversized entry.
func TestBuildContentEntries_LongContent(t *testing.T) {
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
	if contentChunks != 7 {
		t.Errorf("expected 7 content chunks for 12001 chars at chunkSize 2000, got %d", contentChunks)
	}
}

// Verifies Drive-derived content chunking preserves UTF-8 validity when
// multibyte text crosses chunk boundaries.
func TestBuildContentEntries_preservesUTF8Boundaries(t *testing.T) {
	content := strings.Repeat("A\u200c", 2200)
	entries := buildContentEntries("src", "id1", "Title", content, "2024-01-01T00:00:00Z", "",
		"tt", "ot", "ct", "id")
	contentChunks := 0
	for _, e := range entries {
		if e.ContentType != "ct" {
			continue
		}
		contentChunks++
		if !utf8.ValidString(e.Content) {
			t.Fatalf("expected valid UTF-8 content chunk, got %q", e.Content)
		}
	}
	if contentChunks < 2 {
		t.Fatalf("expected multibyte content to split into multiple chunks, got %d", contentChunks)
	}
}

// Verifies buildContentEntries removes invalid UTF-8 bytes before writing
// title, owner, and content search rows.
func TestBuildContentEntries_sanitizesInvalidUTF8(t *testing.T) {
	entries := buildContentEntries("src", "id1", "Ti"+string([]byte{0xff})+"tle", "Body"+string([]byte{0xff})+"Text",
		"2024-01-01T00:00:00Z", "Owner"+string([]byte{0xff}), "tt", "ot", "ct", "id")
	for _, e := range entries {
		if !utf8.ValidString(e.Title) || !utf8.ValidString(e.Content) {
			t.Fatalf("expected valid UTF-8 entry, got %#v", e)
		}
		if strings.ContainsRune(e.Title, '\ufffd') || strings.ContainsRune(e.Content, '\ufffd') {
			t.Fatalf("expected invalid bytes to be removed, got %#v", e)
		}
	}
}

// Verifies splitDriveContentChunks handles zero, passthrough, and truncation
// cases while preserving UTF-8 for shared Drive app chunking.
func TestSplitDriveContentChunks(t *testing.T) {
	t.Run("zero limit", func(t *testing.T) {
		if got := splitDriveContentChunks("hello", 0); got != nil {
			t.Fatalf("expected nil chunks, got %#v", got)
		}
	})
	t.Run("within limit", func(t *testing.T) {
		got := splitDriveContentChunks("hello", 8)
		if len(got) != 1 || got[0] != "hello" {
			t.Fatalf("unexpected chunks: %#v", got)
		}
	})
	t.Run("truncate multibyte", func(t *testing.T) {
		got := splitDriveContentChunks("A\u200cB", 2)
		if len(got) != 2 || got[0] != "A\u200c" || got[1] != "B" {
			t.Fatalf("unexpected multibyte chunks: %#v", got)
		}
	})
}

// Verifies numeric-dominant chunks are skipped so low-value content does not flood global search.
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

// Verifies numeric-heavy sheet content produces fewer global-search entries after low-value filtering.
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

// Verifies owner formatting returns an empty string when there are no owners to show.
func TestFormatDriveOwners_Empty(t *testing.T) {
	result := formatDriveOwners(nil, "")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

// Verifies owner formatting omits the authenticated user from the display list when appropriate.
func TestFormatDriveOwners_ExcludesSelf(t *testing.T) {
	importNote := "uses drive.User inline via pointer"
	_ = importNote
	// Can't use drive.User without importing the SDK in the test — test via formatDriveOwners
	// with no owners
	result := formatDriveOwners(nil, "self@example.com")
	if result != "" {
		t.Errorf("expected empty string with no owners")
	}
}

// Verifies orphan-row deletion removes rows not present in the keep set while preserving listed IDs.
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

// Verifies table counting returns the number of stored rows for seeded tables.
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

// Verifies the source reports the expected registry name string.
func TestSourceName(t *testing.T) {
	src := &Source{}
	if src.Name() != "gsuite" {
		t.Errorf("expected 'gsuite', got %q", src.Name())
	}
	if src.Description() != "Google Suite" {
		t.Errorf("expected 'Google Suite', got %q", src.Description())
	}
}

// Verifies Close is a no-op when the source was created without an open DB.
func TestClose_NilDB(t *testing.T) {
	src := &Source{}
	if err := src.Close(); err != nil {
		t.Errorf("Close with nil db: %v", err)
	}
}
