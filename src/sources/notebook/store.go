package notebook

import (
	"database/sql"
	"time"
)

// CacheRow holds the extracted data for one file as stored in the file_cache table.
type CacheRow struct {
	Path     string
	Dir      string
	ModTime  int64
	Size     int64
	FileType string
	Title    string
	Content  string
	Labels   string
	CachedAt int64
}

// GetCacheRow returns the cached row for path, or nil if not present.
func GetCacheRow(db *sql.DB, path string) (*CacheRow, error) {
	row := db.QueryRow(
		`SELECT path, dir, mod_time, size, file_type, title, content, labels, cached_at
		 FROM file_cache WHERE path = ?`, path)
	var r CacheRow
	err := row.Scan(&r.Path, &r.Dir, &r.ModTime, &r.Size, &r.FileType, &r.Title, &r.Content, &r.Labels, &r.CachedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil { // nocov — non-ErrNoRows scan failures require DB corruption
		return nil, err
	}
	return &r, nil
}

// UpsertCacheRow inserts or replaces a file cache entry with updated extraction results.
func UpsertCacheRow(db *sql.DB, r CacheRow) error {
	if r.CachedAt == 0 {
		r.CachedAt = time.Now().Unix()
	}
	_, err := db.Exec(
		`INSERT OR REPLACE INTO file_cache
		 (path, dir, mod_time, size, file_type, title, content, labels, cached_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Path, r.Dir, r.ModTime, r.Size, r.FileType, r.Title, r.Content, r.Labels, r.CachedAt)
	return err
}

// AllCachePathsForDir returns all cached paths that belong to a given directory.
func AllCachePathsForDir(db *sql.DB, dir string) ([]string, error) {
	rows, err := db.Query(`SELECT path FROM file_cache WHERE dir = ?`, dir)
	if err != nil { // nocov — query on valid schema cannot fail
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil { // nocov — single TEXT column scan
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// PruneStaleEntries removes cache rows for paths that are no longer in activePaths, keeping only live files.
func PruneStaleEntries(db *sql.DB, dir string, activePaths map[string]bool) error {
	cached, err := AllCachePathsForDir(db, dir)
	if err != nil { // nocov — AllCachePathsForDir on valid schema
		return err
	}
	for _, p := range cached {
		if !activePaths[p] {
			if _, err := db.Exec(`DELETE FROM file_cache WHERE path = ?`, p); err != nil { // nocov
				return err
			}
		}
	}
	return nil
}

// PruneDir removes all cache rows for a directory that has been de-configured.
func PruneDir(db *sql.DB, dir string) error {
	_, err := db.Exec(`DELETE FROM file_cache WHERE dir = ?`, dir)
	return err
}
