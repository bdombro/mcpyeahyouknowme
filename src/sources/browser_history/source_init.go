package browser_history

import (
	"database/sql"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // register SQLite driver for browser history snapshot reads
)

const snapshotFallbackRefreshInterval = 60 * time.Second

// NewSource builds a browser history source with macOS path resolution and local snapshot defaults.
func NewSource(dataDir string) *Source {
	snapshotPath := filepath.Join(dataDir, "browser_history.db")
	return &Source{
		dataDir:         dataDir,
		snapshotPath:    snapshotPath,
		resolvePath:     resolveHistoryPath,
		copySnapshot:    copyHistorySnapshot,
		openSnapshotDB:  openSnapshotDB,
		fallbackRefresh: snapshotFallbackRefreshInterval,
	}
}

// Opens a browser history snapshot in read-only SQLite mode for MCP reads and indexing.
func openSnapshotDB(snapshotPath string) (*sql.DB, error) {
	return openReadOnlyDB(snapshotPath)
}
