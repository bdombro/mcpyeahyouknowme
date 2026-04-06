package core

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// Registers the pure-Go SQLite driver used by reset-only search cleanup helpers.
	_ "modernc.org/sqlite"
)

// ClearSearchSource removes one source's rows from search.db so source-local reset commands stop returning stale search hits.
func ClearSearchSource(dataDir, source string) error {
	db, err := openSearchResetDB(dataDir)
	if err != nil {
		return err
	}
	if db == nil {
		return nil
	}
	defer db.Close()

	if _, err := db.Exec(`DELETE FROM search_entries WHERE source = ?`, source); err != nil {
		if strings.Contains(err.Error(), "no such table: search_entries") {
			return nil
		}
		return fmt.Errorf("clear %s from search index: %w", source, err) // nocov
	}
	return nil
}

// Opens search.db for reset-only cleanup, returning nil when the index has not been created yet.
func openSearchResetDB(dataDir string) (*sql.DB, error) {
	dbPath := filepath.Join(dataDir, "search.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat search index: %w", err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(on)", dbPath))
	if err != nil { // nocov
		return nil, fmt.Errorf("open search index: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	return db, nil
}
