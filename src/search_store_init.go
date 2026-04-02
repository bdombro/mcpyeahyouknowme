package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// NewSearchStore opens or creates search.db for daemon/MCP/CLI ownership, applying WAL and busy-timeout settings before use.
func NewSearchStore(dir string, embedder EmbedderInterface) (*SearchStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create search dir: %w", err)
	}

	dbPath := filepath.Join(dir, "search.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open search db: %w", err)
	}
	db.SetMaxOpenConns(1)

	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	db.Exec("PRAGMA mmap_size=268435456")

	if err := initSearchSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &SearchStore{db: db, embedder: embedder}, nil
}

// NewSearchStoreFromDB creates a SearchStore from an existing *sql.DB (for tests).
func NewSearchStoreFromDB(db *sql.DB, embedder EmbedderInterface) (*SearchStore, error) {
	if err := initSearchSchema(db); err != nil {
		return nil, err
	}
	return &SearchStore{db: db, embedder: embedder}, nil
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
		return fmt.Errorf("create search_fts: %w (hint: build with -tags sqlite_fts5)", err)
	}

	_, err = db.Exec(`CREATE TRIGGER IF NOT EXISTS search_fts_insert AFTER INSERT ON search_entries BEGIN
		INSERT INTO search_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
	END`)
	if err != nil {
		return fmt.Errorf("create search_fts_insert trigger: %w", err)
	}

	_, err = db.Exec(`CREATE TRIGGER IF NOT EXISTS search_fts_delete AFTER DELETE ON search_entries BEGIN
		INSERT INTO search_fts(search_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
	END`)
	if err != nil {
		return fmt.Errorf("create search_fts_delete trigger: %w", err)
	}

	_, err = db.Exec(`CREATE TRIGGER IF NOT EXISTS search_fts_update AFTER UPDATE ON search_entries BEGIN
		INSERT INTO search_fts(search_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
		INSERT INTO search_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
	END`)
	if err != nil {
		return fmt.Errorf("create search_fts_update trigger: %w", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS search_embeddings (
		entry_id INTEGER PRIMARY KEY REFERENCES search_entries(id) ON DELETE CASCADE,
		embedding BLOB NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create search_embeddings: %w", err)
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
