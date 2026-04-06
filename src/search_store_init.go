package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// NewSearchStore opens or creates search.db for daemon/MCP/CLI ownership, applying WAL and busy-timeout settings before use.
func NewSearchStore(dir string) (*SearchStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create search dir: %w", err)
	}

	dbPath := filepath.Join(dir, "search.db")
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(on)", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open search db: %w", err)
	}
	db.SetMaxOpenConns(1)

	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	db.Exec("PRAGMA mmap_size=67108864")

	if err := initSearchSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &SearchStore{db: db}, nil
}

const (
	searchFTSInsertTriggerSQL = `CREATE TRIGGER IF NOT EXISTS search_fts_insert AFTER INSERT ON search_entries BEGIN
		INSERT INTO search_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
	END`
	searchFTSDeleteTriggerSQL = `CREATE TRIGGER IF NOT EXISTS search_fts_delete AFTER DELETE ON search_entries BEGIN
		INSERT INTO search_fts(search_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
	END`
	searchFTSUpdateTriggerSQL = `CREATE TRIGGER IF NOT EXISTS search_fts_update AFTER UPDATE ON search_entries BEGIN
		INSERT INTO search_fts(search_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
		INSERT INTO search_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
	END`
)

var searchStoreExecSQL = func(db *sql.DB, statement string) error {
	_, err := db.Exec(statement)
	return err
}

// NewSearchStoreFromDB creates a SearchStore from an existing *sql.DB (for tests).
func NewSearchStoreFromDB(db *sql.DB) (*SearchStore, error) {
	if err := initSearchSchema(db); err != nil {
		return nil, err
	}
	return &SearchStore{db: db}, nil
}

// initSearchSchema creates the search tables, FTS triggers, and metadata tables used by indexing and queries.
func initSearchSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS search_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			source_id TEXT NOT NULL,
			content_type TEXT NOT NULL,
			title TEXT,
			content TEXT NOT NULL,
			metadata TEXT,
			timestamp DATETIME,
			UNIQUE(source, source_id, content_type)
		)`)
	if err != nil {
		return fmt.Errorf("create search_entries: %w", err)
	}

	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
		title, content,
		content='search_entries',
		content_rowid='id'
	)`)
	if err != nil {
		return fmt.Errorf("create search_fts: %w", err)
	}

	if err := createSearchFTSTriggers(db); err != nil {
		return err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS search_meta (
		source TEXT PRIMARY KEY,
		last_indexed DATETIME
	)`)
	if err != nil {
		return fmt.Errorf("create search_meta: %w", err)
	}

	return nil
}

// Restores the external-content FTS maintenance triggers after bulk indexing.
func createSearchFTSTriggers(db *sql.DB) error {
	if err := searchStoreExecSQL(db, searchFTSInsertTriggerSQL); err != nil {
		return fmt.Errorf("create search_fts_insert trigger: %w", err)
	}
	if err := searchStoreExecSQL(db, searchFTSDeleteTriggerSQL); err != nil {
		return fmt.Errorf("create search_fts_delete trigger: %w", err)
	}
	if err := searchStoreExecSQL(db, searchFTSUpdateTriggerSQL); err != nil {
		return fmt.Errorf("create search_fts_update trigger: %w", err)
	}
	return nil
}

// Suspends row-by-row FTS maintenance so full rebuilds can bulk-load entries cheaply.
func dropSearchFTSTriggers(db *sql.DB) error {
	for _, name := range []string{"search_fts_insert", "search_fts_delete", "search_fts_update"} {
		if err := searchStoreExecSQL(db, "DROP TRIGGER IF EXISTS "+name); err != nil {
			return fmt.Errorf("drop %s: %w", name, err)
		}
	}
	return nil
}
