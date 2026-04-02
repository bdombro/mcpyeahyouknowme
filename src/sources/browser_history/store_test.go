package browser_history

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Verifies visit listing supports query filtering and both sort orders with stable pagination.
func TestListVisits_sortAndFilter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	db := newHistoryDB(t, dbPath)
	insertVisit(t, db, 1, 11, "https://example.com/a", "Example A", chromeEpochOffsetMicros+1_000_000, 2)
	insertVisit(t, db, 2, 12, "https://example.com/b", "Example B", chromeEpochOffsetMicros+3_000_000, 1)

	recent, err := listVisits(db, "", "recent", 10, 0)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != 2 || recent[0].VisitID != 12 {
		t.Fatalf("recent order = %+v", recent)
	}

	oldest, err := listVisits(db, "", "oldest", 10, 0)
	if err != nil {
		t.Fatalf("list oldest: %v", err)
	}
	if len(oldest) != 2 || oldest[0].VisitID != 11 {
		t.Fatalf("oldest order = %+v", oldest)
	}

	filtered, err := listVisits(db, "Example A", "recent", 10, 0)
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].VisitID != 11 {
		t.Fatalf("filtered rows = %+v", filtered)
	}
}

// Verifies index row aggregation and SearchEntry construction preserve key metadata fields.
func TestListIndexRowsAndBuildSearchEntries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	db := newHistoryDB(t, dbPath)
	insertVisit(t, db, 9, 99, "https://docs.example.com/path", "", chromeEpochOffsetMicros+5_000_000, 7)

	rows, err := listIndexRows(db)
	if err != nil {
		t.Fatalf("list index rows: %v", err)
	}
	if len(rows) != 1 || rows[0].URLID != 9 {
		t.Fatalf("rows = %+v", rows)
	}

	entries := buildSearchEntries(rows)
	if len(entries) != 1 {
		t.Fatalf("entries len = %d", len(entries))
	}
	if entries[0].ContentType != "browser_visit" || entries[0].SourceID != "9" {
		t.Fatalf("entry = %+v", entries[0])
	}
	if entries[0].Title != "https://docs.example.com/path" {
		t.Fatalf("fallback title = %q", entries[0].Title)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(entries[0].Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["domain"] != "docs.example.com" {
		t.Fatalf("domain metadata = %v", meta["domain"])
	}
}

// Verifies helper utilities clamp pagination and parse domains from URLs.
func TestStoreHelpers(t *testing.T) {
	limit, offset := normalizePagination(0, -4)
	if limit != 50 || offset != 0 {
		t.Fatalf("normalize default = (%d,%d)", limit, offset)
	}
	limit, offset = normalizePagination(300, 3)
	if limit != 200 || offset != 3 {
		t.Fatalf("normalize max = (%d,%d)", limit, offset)
	}

	if got := domainFromURL("https://sub.example.com/path?q=1"); got != "sub.example.com" {
		t.Fatalf("domainFromURL = %q", got)
	}
	if got := domainFromURL(""); got != "" {
		t.Fatalf("domainFromURL empty = %q", got)
	}
}

// Verifies read-only snapshot open succeeds for existing DB paths.
func TestOpenReadOnlyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	db := newHistoryDB(t, dbPath)
	insertVisit(t, db, 1, 2, "https://example.com", "Example", chromeEpochOffsetMicros+2_000, 1)
	_ = db.Close()

	ro, err := openReadOnlyDB(dbPath)
	if err != nil {
		t.Fatalf("openReadOnlyDB: %v", err)
	}
	_ = ro.Close()
}

// Verifies listVisits surfaces query and scan failures from malformed or closed databases.
func TestListVisits_errorPaths(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "history.db")
		db := newHistoryDB(t, dbPath)
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
		if _, err := listVisits(db, "", "recent", 10, 0); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error", func(t *testing.T) {
		db := newMalformedHistoryDB(t, func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`
				CREATE TABLE urls (
					id INTEGER PRIMARY KEY,
					url TEXT NOT NULL,
					title TEXT,
					visit_count INTEGER NOT NULL DEFAULT 0,
					last_visit_time INTEGER NOT NULL DEFAULT 0
				);
				CREATE TABLE visits (
					id INTEGER PRIMARY KEY,
					url INTEGER NOT NULL,
					visit_time INTEGER NOT NULL
				);
				INSERT INTO urls (id, url, title, visit_count, last_visit_time) VALUES (1, 'https://example.com', NULL, 1, ?);
				INSERT INTO visits (id, url, visit_time) VALUES (1, 1, ?);`, chromeEpochOffsetMicros+1_000, chromeEpochOffsetMicros+1_000); err != nil {
				t.Fatalf("seed malformed db: %v", err)
			}
		})
		if _, err := listVisits(db, "", "recent", 10, 0); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// Verifies listIndexRows surfaces query and scan failures from malformed or closed databases.
func TestListIndexRows_errorPaths(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "history.db")
		db := newHistoryDB(t, dbPath)
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
		if _, err := listIndexRows(db); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error", func(t *testing.T) {
		db := newMalformedHistoryDB(t, func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`
				CREATE TABLE urls (
					id INTEGER PRIMARY KEY,
					url TEXT NOT NULL,
					title TEXT,
					visit_count TEXT,
					last_visit_time INTEGER NOT NULL DEFAULT 0
				);
				CREATE TABLE visits (
					id INTEGER PRIMARY KEY,
					url INTEGER NOT NULL,
					visit_time INTEGER NOT NULL
				);
				INSERT INTO urls (id, url, title, visit_count, last_visit_time) VALUES (1, 'https://example.com', 'Example', 'oops', ?);
				INSERT INTO visits (id, url, visit_time) VALUES (1, 1, ?);`, chromeEpochOffsetMicros+1_000, chromeEpochOffsetMicros+1_000); err != nil {
				t.Fatalf("seed malformed db: %v", err)
			}
		})
		if _, err := listIndexRows(db); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// Builds a throwaway sqlite DB with caller-defined schema/data so store tests can force scan failures.
func newMalformedHistoryDB(t *testing.T, seed func(t *testing.T, db *sql.DB)) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "malformed.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seed(t, db)
	return db
}
