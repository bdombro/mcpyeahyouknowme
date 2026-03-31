package googledocs

import (
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2"
	"google.golang.org/api/docs/v1"
)

func TestInitGoogleDocsDB(t *testing.T) {
	db := newTestGoogleDocsDB(t)

	tables := []string{"documents", "sync_state", "documents_fts"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}

	triggers := []string{"documents_ai", "documents_ad", "documents_au"}
	for _, trigger := range triggers {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='trigger' AND name=?", trigger).Scan(&name)
		if err != nil {
			t.Errorf("trigger %s not created: %v", trigger, err)
		}
	}
}

func TestGoogleDocsSource_SaveToken(t *testing.T) {
	dir := t.TempDir()
	g := NewSource(dir)

	token := &oauth2.Token{
		AccessToken:  "test-token",
		RefreshToken: "refresh-token",
	}

	err := g.saveToken(token)
	_ = err // ignore file save error in unit test

	if g.token == nil {
		t.Error("expected token to be set on struct")
	}
	if g.token.AccessToken != "test-token" {
		t.Errorf("token.AccessToken = %q, expected %q", g.token.AccessToken, "test-token")
	}
}

func TestGoogleDocsSource_IsAuthenticated(t *testing.T) {
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

func TestGoogleDocsSource_SyncTime(t *testing.T) {
	db := newTestGoogleDocsDB(t)
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

func TestExtractDocumentText(t *testing.T) {
	tests := []struct {
		name     string
		doc      *docs.Document
		expected string
	}{
		{
			name: "simple paragraph",
			doc: &docs.Document{
				Body: &docs.Body{
					Content: []*docs.StructuralElement{
						{
							Paragraph: &docs.Paragraph{
								Elements: []*docs.ParagraphElement{
									{
										TextRun: &docs.TextRun{
											Content: "Hello World",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: "Hello World",
		},
		{
			name: "multiple paragraphs",
			doc: &docs.Document{
				Body: &docs.Body{
					Content: []*docs.StructuralElement{
						{
							Paragraph: &docs.Paragraph{
								Elements: []*docs.ParagraphElement{
									{TextRun: &docs.TextRun{Content: "First paragraph\n"}},
								},
							},
						},
						{
							Paragraph: &docs.Paragraph{
								Elements: []*docs.ParagraphElement{
									{TextRun: &docs.TextRun{Content: "Second paragraph\n"}},
								},
							},
						},
					},
				},
			},
			expected: "First paragraph\nSecond paragraph\n",
		},
		{
			name: "mixed elements",
			doc: &docs.Document{
				Body: &docs.Body{
					Content: []*docs.StructuralElement{
						{
							Paragraph: &docs.Paragraph{
								Elements: []*docs.ParagraphElement{
									{TextRun: &docs.TextRun{Content: "Text "}},
									{TextRun: &docs.TextRun{Content: "content"}},
								},
							},
						},
						{
							Paragraph: &docs.Paragraph{
								Elements: []*docs.ParagraphElement{
									{TextRun: nil},
									{TextRun: &docs.TextRun{Content: " more"}},
								},
							},
						},
					},
				},
			},
			expected: "Text content more",
		},
		{
			name: "empty document",
			doc: &docs.Document{
				Body: &docs.Body{
					Content: []*docs.StructuralElement{},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDocumentText(tt.doc)
			if result != tt.expected {
				t.Errorf("extractDocumentText() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestGoogleDocsSource_Name(t *testing.T) {
	g := &Source{}
	if g.Name() != "googledocs" {
		t.Errorf("Name() = %q, expected %q", g.Name(), "googledocs")
	}
}

func TestGoogleDocsSource_Description(t *testing.T) {
	g := &Source{}
	if g.Description() != "Google Docs" {
		t.Errorf("Description() = %q, expected %q", g.Description(), "Google Docs")
	}
}

func TestGoogleDocsSource_RequiresAuth(t *testing.T) {
	g := &Source{}
	if !g.RequiresAuth() {
		t.Error("Source should require auth")
	}
}

func TestGoogleDocsSource_Close(t *testing.T) {
	db := newTestGoogleDocsDB(t)
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

func TestGoogleDocsSource_Close_NilDB(t *testing.T) {
	g := &Source{db: nil}
	if err := g.Close(); err != nil {
		t.Errorf("Close() with nil db should return nil, got: %v", err)
	}
}

func TestGoogleDocsSource_SearchEntries_ClosedDB(t *testing.T) {
	db := newTestGoogleDocsDB(t)
	g := &Source{db: db}
	db.Close()
	_, err := g.SearchEntries()
	if err == nil {
		t.Error("expected error when querying closed db")
	}
}

func TestGoogleDocsSource_SyncTime_NilDB(t *testing.T) {
	g := &Source{db: nil}

	// getLastSyncTime returns zero time when db is nil
	if !g.getLastSyncTime().IsZero() {
		t.Error("expected zero time when db is nil")
	}

	// setLastSyncTime is a no-op when db is nil (should not panic)
	g.setLastSyncTime(time.Now())
}

func TestGoogleDocsSource_SearchEntries_NilDB(t *testing.T) {
	g := &Source{db: nil}
	entries, err := g.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries() error: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries with nil db, got %v", entries)
	}
}

func TestGoogleDocsSource_SearchEntries(t *testing.T) {
	db := newTestGoogleDocsDB(t)
	g := &Source{db: db, dataDir: t.TempDir()}

	// Seed a document
	_, err := db.Exec(`
		INSERT INTO documents (id, title, content, modified_time, created_time, web_view_link, last_synced)
		VALUES ('doc1', 'Test Doc', 'Hello world', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', 'https://docs.google.com/doc1', '2024-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("seed document: %v", err)
	}

	entries, err := g.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries() error: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries (title + content), got %d", len(entries))
	}
	if entries[0].ContentType != "document_title" {
		t.Errorf("first entry ContentType = %q, want document_title", entries[0].ContentType)
	}
	if entries[1].ContentType != "document_content" {
		t.Errorf("second entry ContentType = %q, want document_content", entries[1].ContentType)
	}
}

func TestGoogleDocsSource_GetOAuthConfig(t *testing.T) {
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
		"https://www.googleapis.com/auth/documents.readonly",
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

	if config.RedirectURL != "http://127.0.0.1:8085" {
		t.Errorf("RedirectURL = %q, expected %q", config.RedirectURL, "http://127.0.0.1:8085")
	}
}
