package googlesheets

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestGoogleSheetsInfoLines_noArtifacts(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "no (run") {
		t.Errorf("login: %q", lines[0])
	}
	if !strings.Contains(lines[1], "no database yet") {
		t.Errorf("sheets: %q", lines[1])
	}
}

func TestGoogleSheetsInfoLines_withToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "googlesheets_token.json")
	if err := os.WriteFile(tokenPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[0], "yes") || !strings.Contains(lines[1], "no database yet") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleSheetsInfoLines_withTokenAndEmail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googlesheets_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "googlesheets_email.txt"), []byte("user@gmail.com"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[0], "user@gmail.com") {
		t.Fatalf("expected email in login line, got: %q", lines)
	}
}

func TestGoogleSheetsInfoLines_withDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googlesheets_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "googlesheets.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE spreadsheets (id TEXT PRIMARY KEY); INSERT INTO spreadsheets VALUES ('a'),('b');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[1], "2 synced") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleSheetsInfoLines_countQueryFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googlesheets_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "googlesheets.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Close()

	lines := InfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[1], "0 synced") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleSheetsReset_removesOnlyGoogleSheetsFiles(t *testing.T) {
	dDir := t.TempDir()

	gsToken := filepath.Join(dDir, "googlesheets_token.json")
	gsEmail := filepath.Join(dDir, "googlesheets_email.txt")
	gsDB := filepath.Join(dDir, "googlesheets.db")
	waDB := filepath.Join(dDir, "whatsapp.db")
	msgDB := filepath.Join(dDir, "messages.db")

	for _, f := range []string{gsToken, gsEmail, gsDB, waDB, msgDB} {
		if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	src := NewSource(dDir)
	_ = src.Reset(dDir)

	for _, f := range []string{gsToken, gsEmail, gsDB} {
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

func TestGoogleSheetsReset_toleratesMissingFiles(t *testing.T) {
	dDir := t.TempDir()
	src := NewSource(dDir)
	_ = src.Reset(dDir)
}
