package gsuite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

func openGSuiteDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil { // nocov
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "gsuite.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_busy_timeout=30000", dbPath))
	if err != nil { // nocov — sql.Open only fails on unknown drivers
		return nil, fmt.Errorf("failed to open gsuite database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	if err := initGSuiteDB(db); err != nil { // nocov
		db.Close()
		return nil, err
	}
	return db, nil
}

func initGSuiteDB(db *sql.DB) error {
	// Shared sync state table
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}

	// Initialize all app schemas
	for _, app := range allApps {
		if err := app.initSchema(db); err != nil { // nocov
			return fmt.Errorf("init %s schema: %w", app.name, err)
		}
	}
	return nil
}
