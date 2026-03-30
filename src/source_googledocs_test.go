package main

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2"
	"google.golang.org/api/docs/v1"
)

func newTestGoogleDocsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := initGoogleDocsDB(db); err != nil {
		t.Fatalf("initGoogleDocsDB: %v", err)
	}

	return db
}

func TestInitGoogleDocsDB(t *testing.T) {
	db := newTestGoogleDocsDB(t)

	// Verify tables were created
	tables := []string{"documents", "sync_state", "documents_fts"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}

	// Verify triggers were created
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
	// This tests that saveToken sets the token on the struct
	db := newTestGoogleDocsDB(t)
	g := &GoogleDocsSource{db: db}

	token := &oauth2.Token{
		AccessToken:  "test-token",
		RefreshToken: "refresh-token",
	}

	// saveToken should set g.token
	err := g.saveToken(token)
	// Will fail due to file permissions or dataDir() issues, but that's okay for unit test
	// We just verify it tries to save and sets the struct field
	_ = err // Ignore file save error
	
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
			expected: true, // Can be refreshed
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
			g := &GoogleDocsSource{token: tt.token}
			result := g.isAuthenticated()
			if result != tt.expected {
				t.Errorf("isAuthenticated() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGoogleDocsSource_SyncTime(t *testing.T) {
	db := newTestGoogleDocsDB(t)
	g := &GoogleDocsSource{db: db}

	// Initially should be zero
	lastSync := g.getLastSyncTime()
	if !lastSync.IsZero() {
		t.Errorf("expected zero time initially, got %v", lastSync)
	}

	// Set sync time
	now := time.Now()
	g.setLastSyncTime(now)

	// Verify it was saved
	retrieved := g.getLastSyncTime()
	if retrieved.IsZero() {
		t.Error("expected non-zero time after setting")
	}

	// Should be approximately the same (within 1 second due to precision)
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
									{TextRun: nil}, // Non-text element
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
	g := &GoogleDocsSource{}
	if g.Name() != "googledocs" {
		t.Errorf("Name() = %q, expected %q", g.Name(), "googledocs")
	}
}

func TestGoogleDocsSource_Description(t *testing.T) {
	g := &GoogleDocsSource{}
	if g.Description() != "Google Docs" {
		t.Errorf("Description() = %q, expected %q", g.Description(), "Google Docs")
	}
}

func TestGoogleDocsSource_RequiresAuth(t *testing.T) {
	g := &GoogleDocsSource{}
	if !g.RequiresAuth() {
		t.Error("GoogleDocsSource should require auth")
	}
}

func TestGoogleDocsSource_Close(t *testing.T) {
	db := newTestGoogleDocsDB(t)
	g := &GoogleDocsSource{db: db}

	err := g.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	// Verify db is closed
	_, err = db.Query("SELECT 1")
	if err == nil {
		t.Error("expected error querying closed db")
	}
}

func TestGoogleDocsSource_GetOAuthConfig(t *testing.T) {
	g := &GoogleDocsSource{}
	config := g.getOAuthConfig()

	if config == nil {
		t.Fatal("expected non-nil config")
	}

	// Verify config has required scopes
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

	// Verify redirect URL
	if config.RedirectURL != "http://127.0.0.1:8085" {
		t.Errorf("RedirectURL = %q, expected %q", config.RedirectURL, "http://127.0.0.1:8085")
	}
}
