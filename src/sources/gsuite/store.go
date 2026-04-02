package gsuite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // register SQLite driver for database/sql
)

// openGSuiteDB opens gsuite.db with WAL/busy-timeout settings and initializes shared app schema.
func openGSuiteDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil { // nocov
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "gsuite.db")
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(30000)", dbPath))
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

// initGSuiteDB creates shared sync metadata plus every known app schema up front so app toggles do not require later schema bootstrap.
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
