package core

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// FormatSizeMB renders a byte count as megabytes with one decimal place.
func FormatSizeMB(bytes int64) string {
	if bytes <= 0 {
		return "0.0 MB"
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

// FileGroupSizeBytes returns the size of a SQLite database file plus common
// sidecar files like -wal and -shm when they exist.
func FileGroupSizeBytes(path string) int64 {
	paths := []string{path, path + "-wal", path + "-shm"}
	var total int64
	for _, candidate := range paths {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		total += info.Size()
	}
	return total
}

// SQLiteObjectSizeBytes sums dbstat page sizes for SQLite objects matching the
// provided table or virtual-table prefixes. Shadow tables are included.
func SQLiteObjectSizeBytes(db *sql.DB, prefixes []string) (int64, error) {
	rows, err := db.Query(`SELECT name, SUM(pgsize) FROM dbstat GROUP BY name`)
	if err != nil {
		return approximateSQLiteObjectSizeBytes(db, prefixes)
	}
	defer rows.Close()

	var total int64
	for rows.Next() {
		var name string
		var bytes int64
		if err := rows.Scan(&name, &bytes); err != nil {
			return 0, err
		}
		if matchesSQLiteObject(name, prefixes) {
			total += bytes
		}
	}
	return total, rows.Err()
}

func approximateSQLiteObjectSizeBytes(db *sql.DB, prefixes []string) (int64, error) {
	tableNames, err := sqliteTableNames(db)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, name := range tableNames {
		if !matchesSQLiteObject(name, prefixes) {
			continue
		}
		size, err := approximateSQLiteTableSizeBytes(db, name)
		if err != nil {
			return 0, err
		}
		total += size
	}
	return total, nil
}

func sqliteTableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func approximateSQLiteTableSizeBytes(db *sql.DB, tableName string) (int64, error) {
	columns, err := sqliteTableColumns(db, tableName)
	if err != nil {
		return 0, err
	}
	if len(columns) == 0 {
		return 0, nil
	}

	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, fmt.Sprintf(`IFNULL(LENGTH(CAST("%s" AS TEXT)), 0)`, quoteSQLiteIdentifier(column)))
	}
	query := fmt.Sprintf(`SELECT COALESCE(SUM(%s), 0) FROM "%s"`,
		strings.Join(parts, " + "), quoteSQLiteIdentifier(tableName))

	var total int64
	if err := db.QueryRow(query).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func sqliteTableColumns(db *sql.DB, tableName string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, quoteSQLiteIdentifier(tableName)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func quoteSQLiteIdentifier(value string) string {
	return strings.ReplaceAll(value, `"`, `""`)
}

func matchesSQLiteObject(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if name == prefix || strings.HasPrefix(name, prefix+"_") {
			return true
		}
	}
	return false
}
