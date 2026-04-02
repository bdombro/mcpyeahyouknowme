package core

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Verifies size formatting clamps zero and renders megabytes with one decimal place for status output.
func TestFormatSizeMB(t *testing.T) {
	if got := FormatSizeMB(0); got != "0.0 MB" {
		t.Fatalf("FormatSizeMB(0) = %q", got)
	}
	if got := FormatSizeMB(1572864); got != "1.5 MB" {
		t.Fatalf("FormatSizeMB(1572864) = %q", got)
	}
}

// Verifies grouped SQLite file sizing includes the base DB plus existing WAL and SHM sidecars.
func TestFileGroupSizeBytes(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "data.db")
	if err := os.WriteFile(base, []byte("1234"), 0o600); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := os.WriteFile(base+"-wal", []byte("123"), 0o600); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	if err := os.WriteFile(base+"-shm", []byte("12"), 0o600); err != nil {
		t.Fatalf("write shm: %v", err)
	}
	if got := FileGroupSizeBytes(base); got != 9 {
		t.Fatalf("FileGroupSizeBytes() = %d, want 9", got)
	}
}

// Verifies SQLite object sizing counts matching tables/shadow tables and excludes unrelated objects.
func TestSQLiteObjectSizeBytes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stats.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE docs_documents (id INTEGER PRIMARY KEY, content TEXT);
		CREATE TABLE docs_documents_fts_data (block BLOB);
		CREATE TABLE other_table (content TEXT);
		INSERT INTO docs_documents(content) VALUES ('alpha beta gamma');
		INSERT INTO docs_documents_fts_data(block) VALUES (zeroblob(4096));
		INSERT INTO other_table(content) VALUES ('other');
	`); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	size, err := SQLiteObjectSizeBytes(db, []string{"docs_documents", "docs_documents_fts"})
	if err != nil {
		t.Fatalf("SQLiteObjectSizeBytes: %v", err)
	}
	if size <= 0 {
		t.Fatalf("expected positive size, got %d", size)
	}

	otherSize, err := SQLiteObjectSizeBytes(db, []string{"other_table"})
	if err != nil {
		t.Fatalf("SQLiteObjectSizeBytes(other): %v", err)
	}
	if otherSize <= 0 {
		t.Fatalf("expected positive other size, got %d", otherSize)
	}
	if size <= otherSize {
		t.Fatalf("expected docs-related objects to be larger than unrelated table (%d <= %d)", size, otherSize)
	}
}

// Verifies SQLite object matching includes shadow-table prefixes but excludes unrelated autoindex names.
func TestMatchesSQLiteObject(t *testing.T) {
	if !matchesSQLiteObject("docs_documents_fts_data", []string{"docs_documents_fts"}) {
		t.Fatal("expected shadow table prefix match")
	}
	if matchesSQLiteObject("sqlite_autoindex_docs_documents_1", []string{"docs_documents"}) {
		t.Fatal("did not expect unrelated autoindex name to match")
	}
}
