package notebook

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // register SQLite driver for database/sql
)

// NewSource constructs the notebook source, opening (or creating) the file cache DB.
func NewSource(dataDir string) *Source {
	db, err := openNotebookDB(dataDir)
	if err != nil {
		db = nil
	}
	return &Source{db: db, dataDir: dataDir}
}

// openNotebookDB opens notebook.db with WAL and busy-timeout settings and initializes the schema.
func openNotebookDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil { // nocov
		return nil, err
	}
	dbPath := dataDir + "/notebook.db"
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(30000)", dbPath))
	if err != nil { // nocov — sql.Open only fails on unknown drivers
		return nil, fmt.Errorf("failed to open notebook database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	if err := initNotebookDB(db); err != nil { // nocov
		db.Close()
		return nil, err
	}
	return db, nil
}
