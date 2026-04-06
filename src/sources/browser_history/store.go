package browser_history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mcpyeahyouknowme/core"
)

// VisitRow holds one browser visit row returned by the MCP search/list tool.
type VisitRow struct {
	VisitID   int64  `json:"visit_id"`
	VisitTime string `json:"visit_time"`
	URL       string `json:"url"`
	Title     string `json:"title"`
}

// indexRow holds aggregated per-url history data used to build global search entries.
type indexRow struct {
	URLID          int64
	URL            string
	Title          string
	VisitCount     int
	LastVisitMicro int64
}

// Opens a read-only SQLite handle for a copied browser snapshot path.
func openReadOnlyDB(snapshotPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(30000)", snapshotPath))
	if err != nil { // nocov
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Lists visits with optional query filtering and deterministic sort/offset paging.
func listVisits(db *sql.DB, query, sortOrder string, limit, offset int) ([]VisitRow, error) {
	sortClause := "DESC"
	if sortOrder == "oldest" {
		sortClause = "ASC"
	}

	pattern := "%"
	if strings.TrimSpace(query) != "" {
		pattern = "%" + strings.TrimSpace(query) + "%"
	}

	rows, err := db.Query(`
		SELECT v.id, v.visit_time, u.url, u.title
		FROM visits v
		JOIN urls u ON v.url = u.id
		WHERE (? = '%' OR u.url LIKE ? OR u.title LIKE ?)
		ORDER BY v.visit_time `+sortClause+`
		LIMIT ? OFFSET ?`,
		pattern, pattern, pattern, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]VisitRow, 0, limit)
	for rows.Next() {
		var visitID, visitMicro int64
		var url, title string
		if err := rows.Scan(&visitID, &visitMicro, &url, &title); err != nil {
			return nil, err
		}
		results = append(results, VisitRow{
			VisitID:   visitID,
			VisitTime: chromeMicrosToTime(visitMicro).Format(time.RFC3339),
			URL:       url,
			Title:     title,
		})
	}
	if err := rows.Err(); err != nil { // nocov
		return nil, err
	}
	return results, nil
}

// Lists per-url aggregates using the latest visit time to feed global search indexing.
func listIndexRows(db *sql.DB) ([]indexRow, error) {
	rows, err := db.Query(`
		SELECT
			u.id,
			u.url,
			u.title,
			u.visit_count,
			COALESCE(MAX(v.visit_time), u.last_visit_time) AS last_visit_micro
		FROM urls u
		LEFT JOIN visits v ON v.url = u.id
		GROUP BY u.id, u.url, u.title, u.visit_count, u.last_visit_time`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []indexRow
	for rows.Next() {
		var row indexRow
		if err := rows.Scan(&row.URLID, &row.URL, &row.Title, &row.VisitCount, &row.LastVisitMicro); err != nil {
			return nil, err
		}
		entries = append(entries, row)
	}
	if err := rows.Err(); err != nil { // nocov
		return nil, err
	}
	return entries, nil
}

// Builds global search entries from per-url history rows so browser visits appear in search results.
func buildSearchEntries(rows []indexRow) []core.SearchEntry {
	entries := make([]core.SearchEntry, 0, len(rows))
	for _, row := range rows {
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = strings.TrimSpace(row.URL)
		}
		ts := chromeMicrosToTime(row.LastVisitMicro)
		meta, _ := json.Marshal(map[string]interface{}{
			"url":             row.URL,
			"visit_count":     row.VisitCount,
			"last_visit_time": ts.Format(time.RFC3339),
			"url_id":          row.URLID,
			"domain":          domainFromURL(row.URL),
		})
		entries = append(entries, core.SearchEntry{
			Source:      "browser_history",
			SourceID:    fmt.Sprintf("%d", row.URLID),
			ContentType: "browser_visit",
			Title:       title,
			Content:     row.URL,
			Metadata:    meta,
			Timestamp:   &ts,
		})
	}
	return entries
}

// Returns a best-effort host/domain string from URL content for search metadata hints.
func domainFromURL(url string) string {
	trimmed := strings.TrimSpace(url)
	if trimmed == "" {
		return ""
	}
	withoutScheme := strings.TrimPrefix(strings.TrimPrefix(trimmed, "https://"), "http://")
	host := strings.SplitN(withoutScheme, "/", 2)[0]
	host = strings.SplitN(host, "?", 2)[0]
	host = strings.SplitN(host, ":", 2)[0]
	return strings.TrimSpace(host)
}

// Clamps pagination arguments to keep tool requests bounded and predictable.
func normalizePagination(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
