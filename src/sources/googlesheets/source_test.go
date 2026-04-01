package googlesheets

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2"
	"google.golang.org/api/sheets/v4"
)

func TestInitGoogleSheetsDB(t *testing.T) {
	db := newTestGoogleSheetsDB(t)

	tables := []string{"spreadsheets", "sync_state", "spreadsheets_fts"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}

	triggers := []string{"spreadsheets_ai", "spreadsheets_ad", "spreadsheets_au"}
	for _, trigger := range triggers {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='trigger' AND name=?", trigger).Scan(&name)
		if err != nil {
			t.Errorf("trigger %s not created: %v", trigger, err)
		}
	}
}

func TestGoogleSheetsSource_SaveToken(t *testing.T) {
	dir := t.TempDir()
	g := NewSource(dir)

	token := &oauth2.Token{
		AccessToken:  "test-token",
		RefreshToken: "refresh-token",
	}

	err := g.saveToken(token)
	_ = err

	if g.token == nil {
		t.Error("expected token to be set on struct")
	}
	if g.token.AccessToken != "test-token" {
		t.Errorf("token.AccessToken = %q, expected %q", g.token.AccessToken, "test-token")
	}
}

func TestGoogleSheetsSource_IsAuthenticated(t *testing.T) {
	tests := []struct {
		name     string
		token    *oauth2.Token
		expected bool
	}{
		{
			name:     "nil token",
			token:    nil,
			expected: false,
		},
		{
			name: "valid token",
			token: &oauth2.Token{
				AccessToken: "valid-token",
				Expiry:      time.Now().Add(1 * time.Hour),
			},
			expected: true,
		},
		{
			name: "expired token with refresh",
			token: &oauth2.Token{
				AccessToken:  "expired-token",
				RefreshToken: "refresh-token",
				Expiry:       time.Now().Add(-1 * time.Hour),
			},
			expected: true,
		},
		{
			name: "expired token without refresh",
			token: &oauth2.Token{
				AccessToken: "expired-token",
				Expiry:      time.Now().Add(-1 * time.Hour),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Source{token: tt.token}
			result := g.isAuthenticated()
			if result != tt.expected {
				t.Errorf("isAuthenticated() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGoogleSheetsSource_SyncTime(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db}

	lastSync := g.getLastSyncTime()
	if !lastSync.IsZero() {
		t.Errorf("expected zero time initially, got %v", lastSync)
	}

	now := time.Now()
	g.setLastSyncTime(now)

	retrieved := g.getLastSyncTime()
	if retrieved.IsZero() {
		t.Error("expected non-zero time after setting")
	}

	if retrieved.Sub(now).Abs() > time.Second {
		t.Errorf("time mismatch: got %v, expected ~%v", retrieved, now)
	}
}

func TestExtractSpreadsheetText(t *testing.T) {
	tests := []struct {
		name     string
		ss       *sheets.Spreadsheet
		expected string
	}{
		{
			name: "single sheet with data",
			ss: &sheets.Spreadsheet{
				Sheets: []*sheets.Sheet{
					{
						Properties: &sheets.SheetProperties{Title: "Sheet1"},
						Data: []*sheets.GridData{
							{
								RowData: []*sheets.RowData{
									{Values: []*sheets.CellData{
										{FormattedValue: "A"},
										{FormattedValue: "B"},
									}},
									{Values: []*sheets.CellData{
										{FormattedValue: "1"},
										{FormattedValue: "2"},
									}},
								},
							},
						},
					},
				},
			},
			expected: "## Sheet1\nA\tB\n1\t2\n",
		},
		{
			name: "multiple sheets",
			ss: &sheets.Spreadsheet{
				Sheets: []*sheets.Sheet{
					{
						Properties: &sheets.SheetProperties{Title: "Data"},
						Data: []*sheets.GridData{
							{
								RowData: []*sheets.RowData{
									{Values: []*sheets.CellData{
										{FormattedValue: "X"},
									}},
								},
							},
						},
					},
					{
						Properties: &sheets.SheetProperties{Title: "Summary"},
						Data: []*sheets.GridData{
							{
								RowData: []*sheets.RowData{
									{Values: []*sheets.CellData{
										{FormattedValue: "Total"},
										{FormattedValue: "100"},
									}},
								},
							},
						},
					},
				},
			},
			expected: "## Data\nX\n\n## Summary\nTotal\t100\n",
		},
		{
			name: "sheet with no data",
			ss: &sheets.Spreadsheet{
				Sheets: []*sheets.Sheet{
					{
						Properties: &sheets.SheetProperties{Title: "Empty"},
						Data:       nil,
					},
				},
			},
			expected: "## Empty\n",
		},
		{
			name: "empty spreadsheet",
			ss: &sheets.Spreadsheet{
				Sheets: []*sheets.Sheet{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSpreadsheetText(tt.ss)
			if result != tt.expected {
				t.Errorf("extractSpreadsheetText() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestGoogleSheetsSource_Name(t *testing.T) {
	g := &Source{}
	if g.Name() != "googlesheets" {
		t.Errorf("Name() = %q, expected %q", g.Name(), "googlesheets")
	}
}

func TestGoogleSheetsSource_Description(t *testing.T) {
	g := &Source{}
	if g.Description() != "Google Sheets" {
		t.Errorf("Description() = %q, expected %q", g.Description(), "Google Sheets")
	}
}

func TestGoogleSheetsSource_RequiresAuth(t *testing.T) {
	g := &Source{}
	if !g.RequiresAuth() {
		t.Error("Source should require auth")
	}
}

func TestGoogleSheetsSource_Close(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db}

	err := g.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	_, err = db.Query("SELECT 1")
	if err == nil {
		t.Error("expected error querying closed db")
	}
}

func TestGoogleSheetsSource_Close_NilDB(t *testing.T) {
	g := &Source{db: nil}
	if err := g.Close(); err != nil {
		t.Errorf("Close() with nil db should return nil, got: %v", err)
	}
}

func TestGoogleSheetsSource_SearchEntries_ClosedDB(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db}
	db.Close()
	_, err := g.SearchEntries()
	if err == nil {
		t.Error("expected error when querying closed db")
	}
}

func TestGoogleSheetsSource_SyncTime_NilDB(t *testing.T) {
	g := &Source{db: nil}

	if !g.getLastSyncTime().IsZero() {
		t.Error("expected zero time when db is nil")
	}

	g.setLastSyncTime(time.Now())
}

func TestGoogleSheetsSource_SearchEntries_NilDB(t *testing.T) {
	g := &Source{db: nil}
	entries, err := g.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries() error: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries with nil db, got %v", entries)
	}
}

func TestGoogleSheetsSource_SearchEntries(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db, dataDir: t.TempDir()}

	_, err := db.Exec(`
		INSERT INTO spreadsheets (id, title, content, modified_time, created_time, web_view_link, owners, sheet_count, last_synced)
		VALUES ('ss1', 'Test Sheet', '## Sheet1\nHello world\n', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', 'https://docs.google.com/spreadsheets/d/ss1', 'Alice <alice@example.com>', 1, '2024-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("seed spreadsheet: %v", err)
	}

	entries, err := g.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries() error: %v", err)
	}
	if len(entries) < 3 {
		t.Fatalf("expected at least 3 entries (title + owner + content), got %d", len(entries))
	}
	if entries[0].ContentType != "spreadsheet_title" {
		t.Errorf("first entry ContentType = %q, want spreadsheet_title", entries[0].ContentType)
	}
	if !strings.Contains(entries[0].Content, "Alice") {
		t.Errorf("title entry Content should contain owner name, got: %s", entries[0].Content)
	}
	if entries[1].ContentType != "spreadsheet_owner" {
		t.Errorf("second entry ContentType = %q, want spreadsheet_owner", entries[1].ContentType)
	}
	if entries[1].Content != "Alice <alice@example.com>" {
		t.Errorf("owner entry Content = %q, want %q", entries[1].Content, "Alice <alice@example.com>")
	}
	if entries[2].ContentType != "spreadsheet_content" {
		t.Errorf("third entry ContentType = %q, want spreadsheet_content", entries[2].ContentType)
	}
	if !strings.Contains(entries[2].Content, "Owners: Alice") {
		t.Errorf("content entry should start with owners, got: %s", entries[2].Content)
	}
}

func TestGoogleSheetsSource_GetOAuthConfig(t *testing.T) {
	origID := GoogleClientID
	origSecret := GoogleClientSecret
	defer func() { GoogleClientID = origID; GoogleClientSecret = origSecret }()

	GoogleClientID = "test-id"
	GoogleClientSecret = "test-secret"

	g := &Source{}
	config := g.getOAuthConfig()

	if config == nil {
		t.Fatal("expected non-nil config")
	}

	if config.ClientID != "test-id" {
		t.Errorf("ClientID = %q, want %q", config.ClientID, "test-id")
	}
	if config.ClientSecret != "test-secret" {
		t.Errorf("ClientSecret = %q, want %q", config.ClientSecret, "test-secret")
	}

	expectedScopes := []string{
		"https://www.googleapis.com/auth/spreadsheets.readonly",
		"https://www.googleapis.com/auth/drive.readonly",
	}

	if len(config.Scopes) != len(expectedScopes) {
		t.Errorf("expected %d scopes, got %d", len(expectedScopes), len(config.Scopes))
	}

	for i, scope := range expectedScopes {
		if i >= len(config.Scopes) || config.Scopes[i] != scope {
			t.Errorf("scope[%d] = %q, expected %q", i, config.Scopes[i], scope)
		}
	}

	if config.RedirectURL != "http://127.0.0.1:8086" {
		t.Errorf("RedirectURL = %q, expected %q", config.RedirectURL, "http://127.0.0.1:8086")
	}
}

func TestFormatOwners(t *testing.T) {
	tests := []struct {
		name      string
		owners    []sheetOwner
		selfEmail string
		expected  string
	}{
		{
			name:     "single owner",
			owners:   []sheetOwner{{DisplayName: "Alice", EmailAddress: "alice@example.com"}},
			expected: "Alice <alice@example.com>",
		},
		{
			name: "multiple owners",
			owners: []sheetOwner{
				{DisplayName: "Alice", EmailAddress: "alice@example.com"},
				{DisplayName: "Bob", EmailAddress: "bob@example.com"},
			},
			expected: "Alice <alice@example.com>, Bob <bob@example.com>",
		},
		{
			name:      "exclude self",
			owners:    []sheetOwner{{DisplayName: "Me", EmailAddress: "me@example.com"}},
			selfEmail: "me@example.com",
			expected:  "",
		},
		{
			name:     "email only",
			owners:   []sheetOwner{{EmailAddress: "anon@example.com"}},
			expected: "anon@example.com",
		},
		{
			name:     "name only",
			owners:   []sheetOwner{{DisplayName: "NoEmail"}},
			expected: "NoEmail",
		},
		{
			name:     "empty owner",
			owners:   []sheetOwner{{}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatOwners(tt.owners, tt.selfEmail)
			if result != tt.expected {
				t.Errorf("formatOwners() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGoogleSheetsSource_DeleteOrphanedSpreadsheets(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db}

	db.Exec(`
		INSERT INTO spreadsheets (id, title, content, modified_time, created_time, owners, sheet_count, last_synced)
		VALUES
		  ('keep1', 'Keep', '', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '', 1, '2024-01-01T00:00:00Z'),
		  ('del1', 'Delete', '', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '', 1, '2024-01-01T00:00:00Z')
	`)

	remoteIDs := map[string]bool{"keep1": true}
	deleted, err := g.deleteOrphanedSpreadsheets(remoteIDs)
	if err != nil {
		t.Fatalf("deleteOrphanedSpreadsheets error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM spreadsheets").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 remaining spreadsheet, got %d", count)
	}
}

func TestGoogleSheetsSource_GetLocalModifiedTime(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db}

	if got := g.getLocalModifiedTime("nonexistent"); got != "" {
		t.Errorf("expected empty string for missing row, got %q", got)
	}

	db.Exec(`
		INSERT INTO spreadsheets (id, title, content, modified_time, created_time, owners, sheet_count, last_synced)
		VALUES ('ss1', 'T', '', '2024-06-01T00:00:00Z', '2024-01-01T00:00:00Z', '', 1, '2024-01-01T00:00:00Z')
	`)
	if got := g.getLocalModifiedTime("ss1"); got != "2024-06-01T00:00:00Z" {
		t.Errorf("getLocalModifiedTime = %q, want %q", got, "2024-06-01T00:00:00Z")
	}
}

func TestGoogleSheetsSource_DeleteOrphanedSpreadsheets_ClosedDB(t *testing.T) {
	db, _ := sql.Open("sqlite3", "file::memory:?cache=shared")
	initGoogleSheetsDB(db)
	g := &Source{db: db}
	db.Close()

	_, err := g.deleteOrphanedSpreadsheets(map[string]bool{})
	if err == nil {
		t.Error("expected error when db is closed")
	}
}
