package notebook

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
)

// init registers the notebook source name so config normalization keeps a stable entry for it.
func init() {
	core.RegisterKnownSource("notebook")
}

// Source implements core.DataSource for local markdown, PDF, and image file indexing.
type Source struct {
	db      *sql.DB
	dataDir string
}

//revive:disable:exported
// NotebookConfig lists directories the notebook source walks on each daemon SearchEntries tick; entries land in notebook.db
// and notebook_* tools (docs/spec.md Notebook File Indexing).
type NotebookConfig struct {
	Dirs []string `json:"dirs"`
}

//revive:enable:exported

// IsConfigured reports whether at least one notebook directory has been configured.
func IsConfigured(dataDir string) bool {
	cfg := loadNotebookConfig(dataDir)
	return len(cfg.Dirs) > 0
}

// loadNotebookConfig reads the notebook source config from config.json.
func loadNotebookConfig(dataDir string) NotebookConfig {
	sc := core.LoadConfig(dataDir).Sources["notebook"]
	if len(sc.Auth) == 0 {
		return NotebookConfig{}
	}
	var cfg NotebookConfig
	if err := json.Unmarshal(sc.Auth, &cfg); err != nil {
		return NotebookConfig{}
	}
	return cfg
}

// saveNotebookConfig persists the notebook source config into config.json.
func saveNotebookConfig(dataDir string, cfg NotebookConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil { // nocov — NotebookConfig always marshals cleanly
		return err
	}
	return core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		sc.Auth = data
	})
}

// Name returns the source key used for registry lookup and tool prefixes.
func (s *Source) Name() string { return "notebook" }

// Description returns the human label shown in CLI and status output.
func (s *Source) Description() string { return "Notebook" }

// Close releases the SQLite cache connection.
func (s *Source) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Reset removes notebook.db and clears the source config, leaving user files untouched.
func (s *Source) Reset(dataDir string) error {
	return core.DefaultReset(dataDir, []string{
		"notebook.db",
		"notebook.db-wal",
		"notebook.db-shm",
	})
}

// RegisterTools adds the notebook read tools to the MCP server.
func (s *Source) RegisterTools(srv *server.MCPServer) {
	registerTools(s, srv)
}

// SearchEntries walks all configured directories, checking the file cache to avoid re-extracting unchanged files.
func (s *Source) SearchEntries() ([]core.SearchEntry, error) {
	if s.db == nil {
		return nil, nil
	}
	cfg := loadNotebookConfig(s.dataDir)
	if len(cfg.Dirs) == 0 {
		return nil, nil
	}
	return scanDirs(s.db, cfg.Dirs, s.Name())
}

// InfoLines returns indented status lines for the `info` command notebook section.
func InfoLines(dataDir string) []string {
	sc := core.LoadConfig(dataDir).Sources["notebook"]
	if !sc.Enabled {
		return []string{"   Status:     disabled"}
	}
	cfg := loadNotebookConfig(dataDir)
	if len(cfg.Dirs) == 0 {
		return []string{
			"   Status:     enabled (no directories configured)",
			"   Hint:       run 'mcpyeahyouknowme notebook add <path>'",
		}
	}
	lines := []string{"   Status:     enabled"}
	for _, dir := range cfg.Dirs {
		counts := countFilesInDir(dir)
		lines = append(lines, formatDirLine(dir, counts))
	}
	dbSize := core.FileGroupSizeBytes(filepath.Join(dataDir, "notebook.db"))
	if dbSize > 0 {
		lines = append(lines, "   Cache:      "+core.FormatSizeMB(dbSize))
	}
	return lines
}

// countFilesInDir counts .md, .pdf, and image files in a directory without opening them.
func countFilesInDir(dir string) map[string]int {
	counts := map[string]int{"md": 0, "pdf": 0, "image": 0}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return counts
	}
	// Simple top-level count for info display; full walk happens in scanner.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch fileTypeOf(e.Name()) {
		case "md":
			counts["md"]++
		case "pdf":
			counts["pdf"]++
		case "image":
			counts["image"]++
		}
	}
	return counts
}

// formatDirLine builds the indented info line for one configured directory.
func formatDirLine(dir string, counts map[string]int) string {
	return "   Dir:        " + dir +
		" (" + itoa(counts["md"]) + " md, " +
		itoa(counts["pdf"]) + " pdf, " +
		itoa(counts["image"]) + " images)"
}

// itoa converts an int to string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
