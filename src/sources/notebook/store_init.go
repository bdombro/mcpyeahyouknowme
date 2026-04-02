package notebook

import "database/sql"

// initNotebookDB creates the file_cache table and index if they do not already exist.
func initNotebookDB(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS file_cache (
		path      TEXT PRIMARY KEY,
		dir       TEXT NOT NULL,
		mod_time  INTEGER NOT NULL,
		size      INTEGER NOT NULL,
		file_type TEXT NOT NULL,
		title     TEXT NOT NULL DEFAULT '',
		content   TEXT NOT NULL DEFAULT '',
		labels    TEXT NOT NULL DEFAULT '[]',
		cached_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_file_cache_dir ON file_cache(dir);
	`)
	return err
}
