package googlesheets

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mcpyeahyouknowme/core"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2"
)

// Source implements core.DataSource and core.CoreService for Google Sheets.
type Source struct {
	db      *sql.DB
	token   *oauth2.Token
	dataDir string
}

// NewSource creates a new Google Sheets source rooted at dataDir.
func NewSource(dataDir string) *Source {
	db, err := openGoogleSheetsDB(dataDir)
	if err != nil {
		db = nil
	}
	src := &Source{db: db, dataDir: dataDir}
	src.loadToken()
	return src
}

func (g *Source) Name() string        { return "googlesheets" }
func (g *Source) Description() string { return "Google Sheets" }
func (g *Source) Close() error {
	if g.db != nil {
		return g.db.Close()
	}
	return nil
}

// Reset removes all Google Sheets data files.
func (g *Source) Reset(dataDir string) error {
	return core.DefaultReset(dataDir, []string{
		"googlesheets.db",
		"googlesheets.db-wal",
		"googlesheets.db-shm",
		"googlesheets_token.json",
		"googlesheets_email.txt",
	})
}

func openGoogleSheetsDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil { // nocov
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "googlesheets.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_busy_timeout=30000", dbPath))
	if err != nil { // nocov — sql.Open only fails on unknown drivers
		return nil, fmt.Errorf("failed to open googlesheets database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	if err := initGoogleSheetsDB(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func initGoogleSheetsDB(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS spreadsheets (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		modified_time TEXT NOT NULL,
		created_time TEXT NOT NULL,
		web_view_link TEXT,
		owners TEXT NOT NULL DEFAULT '',
		sheet_count INTEGER NOT NULL DEFAULT 0,
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}

	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS spreadsheets_fts USING fts5(
		title, content, owners,
		content='spreadsheets',
		content_rowid='rowid'
	);

	CREATE TRIGGER IF NOT EXISTS spreadsheets_ai AFTER INSERT ON spreadsheets BEGIN
		INSERT INTO spreadsheets_fts(rowid, title, content, owners)
		VALUES (new.rowid, new.title, new.content, new.owners);
	END;

	CREATE TRIGGER IF NOT EXISTS spreadsheets_ad AFTER DELETE ON spreadsheets BEGIN
		DELETE FROM spreadsheets_fts WHERE rowid = old.rowid;
	END;

	CREATE TRIGGER IF NOT EXISTS spreadsheets_au AFTER UPDATE ON spreadsheets BEGIN
		INSERT INTO spreadsheets_fts(spreadsheets_fts, rowid, title, content, owners) VALUES('delete', old.rowid, old.title, old.content, old.owners);
		INSERT INTO spreadsheets_fts(rowid, title, content, owners) VALUES (new.rowid, new.title, new.content, new.owners);
	END;
	`)
	if err != nil { // nocov
		return err
	}

	db.Exec("INSERT INTO spreadsheets_fts(spreadsheets_fts) VALUES('rebuild')")
	return nil
}

// SearchEntries returns all spreadsheets for the global search index.
func (g *Source) SearchEntries() ([]core.SearchEntry, error) {
	if g.db == nil {
		return nil, nil
	}
	rows, err := g.db.Query(`
		SELECT id, title, content, modified_time, owners
		FROM spreadsheets
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []core.SearchEntry
	for rows.Next() {
		var id, title, content, modifiedTime, owners string
		if err := rows.Scan(&id, &title, &content, &modifiedTime, &owners); err != nil { // nocov
			continue
		}

		baseMeta := map[string]interface{}{
			"spreadsheet_id": id,
			"modified_time":  modifiedTime,
		}
		if owners != "" {
			baseMeta["owners"] = owners
		}

		indexedTitle := title
		if owners != "" {
			indexedTitle = owners + " — " + title
		}

		metadata, _ := json.Marshal(baseMeta)
		entries = append(entries, core.SearchEntry{
			Source:      g.Name(),
			SourceID:    id,
			ContentType: "spreadsheet_title",
			Title:       title,
			Content:     indexedTitle,
			Metadata:    metadata,
		})

		if owners != "" {
			ownerMeta, _ := json.Marshal(baseMeta)
			entries = append(entries, core.SearchEntry{
				Source:      g.Name(),
				SourceID:    id,
				ContentType: "spreadsheet_owner",
				Title:       title,
				Content:     owners,
				Metadata:    ownerMeta,
			})
		}

		if len(content) > 0 {
			contentWithOwners := content
			if owners != "" {
				contentWithOwners = "Owners: " + owners + "\n\n" + content
			}
			chunkSize := 5000
			for i := 0; i < len(contentWithOwners); i += chunkSize {
				end := i + chunkSize
				if end > len(contentWithOwners) {
					end = len(contentWithOwners)
				}
				chunk := contentWithOwners[i:end]

				chunkMeta, _ := json.Marshal(map[string]interface{}{
					"spreadsheet_id":    id,
					"spreadsheet_title": title,
					"chunk_index":       i / chunkSize,
					"modified_time":     modifiedTime,
				})
				entries = append(entries, core.SearchEntry{
					Source:      g.Name(),
					SourceID:    id,
					ContentType: "spreadsheet_content",
					Title:       title,
					Content:     chunk,
					Metadata:    chunkMeta,
				})
			}
		}
	}
	return entries, nil
}

// formatOwners returns a comma-separated "Name <email>" string for each owner,
// excluding the currently logged-in user (by email).
func formatOwners(owners []sheetOwner, selfEmail string) string {
	parts := make([]string, 0, len(owners))
	for _, o := range owners {
		if selfEmail != "" && strings.EqualFold(o.EmailAddress, selfEmail) {
			continue
		}
		if o.DisplayName != "" && o.EmailAddress != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", o.DisplayName, o.EmailAddress))
		} else if o.EmailAddress != "" {
			parts = append(parts, o.EmailAddress)
		} else if o.DisplayName != "" {
			parts = append(parts, o.DisplayName)
		}
	}
	return strings.Join(parts, ", ")
}

// sheetOwner is a minimal owner representation used by formatOwners.
type sheetOwner struct {
	DisplayName  string
	EmailAddress string
}
