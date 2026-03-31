package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DefaultReset removes a list of files (by relative path under dataDir).
// Missing files are silently skipped. Returns the first non-missing error.
func DefaultReset(dataDir string, files []string) error {
	var firstErr error
	for _, f := range files {
		path := filepath.Join(dataDir, f)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", path, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// RunPollLoop runs fn immediately then on every interval tick until ctx is done.
// Errors from fn are printed but do not stop the loop.
func RunPollLoop(ctx context.Context, interval time.Duration, fn func(context.Context) error) error {
	if err := fn(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Sync error: %v\n", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := fn(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Sync error: %v\n", err)
			}
		}
	}
}

// OpenDB opens a SQLite database at filepath.Join(dataDir, filename) with
// shared pragmas (WAL mode, 30-second busy timeout, foreign keys on).
func OpenDB(dataDir, filename string) (*sql.DB, error) {
	path := filepath.Join(dataDir, filename)
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=30000", path))
	if err != nil {
		return nil, err
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	return db, nil
}
