package googledocs

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestGoogleDocsInfoLines_noArtifacts(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "no (run") {
		t.Errorf("login: %q", lines[0])
	}
	if !strings.Contains(lines[1], "no database yet") {
		t.Errorf("docs: %q", lines[1])
	}
}

func TestGoogleDocsInfoLines_withToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "googledocs_token.json")
	if err := os.WriteFile(tokenPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[0], "yes") || !strings.Contains(lines[1], "no database yet") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleDocsInfoLines_withTokenAndEmail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googledocs_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "googledocs_email.txt"), []byte("user@gmail.com"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[0], "user@gmail.com") {
		t.Fatalf("expected email in login line, got: %q", lines)
	}
}

func TestGoogleDocsInfoLines_withDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googledocs_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "googledocs.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE documents (id TEXT PRIMARY KEY); INSERT INTO documents VALUES ('a'),('b');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[1], "2 synced") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleDocsInfoLines_countQueryFails(t *testing.T) {
	// When the DB exists but has no documents, InfoLines should report "0 synced".
	// The "unable to count" path is no longer reachable since NewSource always
	// initialises the schema, so we test the 0-document case here instead.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googledocs_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Create an empty (but valid) googledocs.db — NewSource will apply the schema.
	dbPath := filepath.Join(dir, "googledocs.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("PRAGMA journal_mode=WAL") // force file creation
	db.Close()

	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[1], "0 synced") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleDocsReset_removesOnlyGoogleDocsFiles(t *testing.T) {
	dDir := t.TempDir()

	gdToken := filepath.Join(dDir, "googledocs_token.json")
	gdEmail := filepath.Join(dDir, "googledocs_email.txt")
	gdDB := filepath.Join(dDir, "googledocs.db")
	waDB := filepath.Join(dDir, "whatsapp.db")
	msgDB := filepath.Join(dDir, "messages.db")

	for _, f := range []string{gdToken, gdEmail, gdDB, waDB, msgDB} {
		if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	src := NewSource(dDir)
	_ = src.Reset(dDir)

	for _, f := range []string{gdToken, gdEmail, gdDB} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", filepath.Base(f))
		}
	}
	for _, f := range []string{waDB, msgDB} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected %s to be preserved, got err: %v", filepath.Base(f), err)
		}
	}
}

func TestGoogleDocsReset_toleratesMissingFiles(t *testing.T) {
	dDir := t.TempDir()
	src := NewSource(dDir)
	_ = src.Reset(dDir) // should not panic or error
}
