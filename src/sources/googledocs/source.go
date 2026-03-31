package googledocs

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

// Source implements core.DataSource and core.CoreService for Google Docs.
type Source struct {
	db      *sql.DB
	token   *oauth2.Token
	dataDir string
}

// NewSource creates a new Google Docs source rooted at dataDir.
func NewSource(dataDir string) *Source {
	db, err := openGoogleDocsDB(dataDir)
	if err != nil {
		// Return a source with no DB; reads will return empty results.
		db = nil
	}
	src := &Source{db: db, dataDir: dataDir}
	src.loadToken() // non-fatal if no token yet
	return src
}

func (g *Source) Name() string        { return "googledocs" }
func (g *Source) Description() string { return "Google Docs" }
func (g *Source) Close() error {
	if g.db != nil {
		return g.db.Close()
	}
	return nil
}

// Reset removes all Google Docs data files.
func (g *Source) Reset(dataDir string) error {
	return core.DefaultReset(dataDir, []string{
		"googledocs.db",
		"googledocs.db-wal",
		"googledocs.db-shm",
		"googledocs_token.json",
		"googledocs_email.txt",
	})
}

func openGoogleDocsDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil { // nocov
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "googledocs.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_busy_timeout=30000", dbPath))
	if err != nil { // nocov — sql.Open only fails on unknown drivers
		return nil, fmt.Errorf("failed to open googledocs database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	if err := initGoogleDocsDB(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func initGoogleDocsDB(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		modified_time TEXT NOT NULL,
		created_time TEXT NOT NULL,
		web_view_link TEXT,
		owners TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil {
		return err
	}
	// Add owners column to existing databases (no-op if already present).
	db.Exec("ALTER TABLE documents ADD COLUMN owners TEXT NOT NULL DEFAULT ''")

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}

	// Migrate FTS5 to include owners: if the existing FTS table lacks the
	// owners column, drop it and its triggers so they're recreated below.
	var ftsSQL string
	_ = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='documents_fts'").Scan(&ftsSQL)
	if ftsSQL != "" && !strings.Contains(ftsSQL, "owners") {
		db.Exec("DROP TABLE IF EXISTS documents_fts")
		db.Exec("DROP TRIGGER IF EXISTS documents_ai")
		db.Exec("DROP TRIGGER IF EXISTS documents_ad")
		db.Exec("DROP TRIGGER IF EXISTS documents_au")
	}

	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
		title, content, owners,
		content='documents',
		content_rowid='rowid'
	);

	CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
		INSERT INTO documents_fts(rowid, title, content, owners)
		VALUES (new.rowid, new.title, new.content, new.owners);
	END;

	CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
		DELETE FROM documents_fts WHERE rowid = old.rowid;
	END;

	CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
		INSERT INTO documents_fts(documents_fts, rowid, title, content, owners) VALUES('delete', old.rowid, old.title, old.content, old.owners);
		INSERT INTO documents_fts(rowid, title, content, owners) VALUES (new.rowid, new.title, new.content, new.owners);
	END;
	`)
	if err != nil { // nocov
		return err
	}

	// Rebuild FTS content after migration or on first run.
	db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('rebuild')")
	return nil
}

// SearchEntries returns all documents for the global search index.
func (g *Source) SearchEntries() ([]core.SearchEntry, error) {
	if g.db == nil {
		return nil, nil
	}
	rows, err := g.db.Query(`
		SELECT id, title, content, modified_time, owners
		FROM documents
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
			"document_id":   id,
			"modified_time": modifiedTime,
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
			ContentType: "document_title",
			Title:       title,
			Content:     indexedTitle,
			Metadata:    metadata,
		})

		if owners != "" {
			ownerMeta, _ := json.Marshal(baseMeta)
			entries = append(entries, core.SearchEntry{
				Source:      g.Name(),
				SourceID:    id,
				ContentType: "document_owner",
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
					"document_id":    id,
					"document_title": title,
					"chunk_index":    i / chunkSize,
					"modified_time":  modifiedTime,
				})
				entries = append(entries, core.SearchEntry{
					Source:      g.Name(),
					SourceID:    id,
					ContentType: "document_content",
					Title:       title,
					Content:     chunk,
					Metadata:    chunkMeta,
				})
			}
		}
	}
	return entries, nil
}
