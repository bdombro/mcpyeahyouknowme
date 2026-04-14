package notebook

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"

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

// NotebookConfig lists directories the notebook source walks on each daemon SearchEntries tick; entries land in notebook.db
// and notebook_* tools (docs/spec.md Notebook File Indexing).
//
//revive:disable:exported
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
func (s *Source) RegisterTools(srv core.ToolAdder) {
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

// HasChangesSince checks the notebook SQLite cache files so incremental daemon
// ticks can skip a full directory walk when cached extraction state is unchanged.
func (s *Source) HasChangesSince(t time.Time) bool {
	if t.IsZero() {
		return true
	}
	latest := latestNotebookDBModTime(s.dataDir)
	if latest.IsZero() {
		return true
	}
	return !latest.Before(t)
}

// InfoLines returns indented status lines for the `info` command notebook section.
func InfoLines(dataDir string) []string {
	sc := core.LoadConfig(dataDir).Sources["notebook"]
	if !sc.Enabled {
		return nil
	}
	cfg := loadNotebookConfig(dataDir)
	if len(cfg.Dirs) == 0 {
		return []string{
			"   Hint:       run 'mcpyeahyouknowme notebook add <path>'",
		}
	}
	var lines []string
	for _, dir := range cfg.Dirs {
		counts := countFilesInDir(dir)
		lines = append(lines, formatDirLines(tildeHome(dir), counts)...)
	}
	dbSize := core.FileGroupSizeBytes(filepath.Join(dataDir, "notebook.db"))
	if dbSize > 0 {
		lines = append(lines, "   DB:         "+core.FormatSizeMB(dbSize))
	}
	return lines
}

// tildeHome replaces the home directory prefix with ~ for shorter path display in status output.
func tildeHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" { // nocov
		return path
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	if path == home {
		return "~"
	}
	return path
}

// countFilesInDir walks a configured directory tree for display counts while matching notebook scan rules.
func countFilesInDir(dir string) map[string]int {
	counts := map[string]int{"md": 0, "pdf": 0, "image": 0}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		switch fileTypeOf(d.Name()) {
		case "md":
			counts["md"]++
		case "pdf":
			counts["pdf"]++
		case "image":
			counts["image"]++
		}
		return nil
	})
	return counts
}

// Builds the indented info lines for one configured directory so long paths and file counts stay readable in CLI output.
func formatDirLines(dir string, counts map[string]int) []string {
	return []string{
		"   Dir:        " + dir,
		"               (" + itoa(counts["md"]) + " md, " +
			itoa(counts["pdf"]) + " pdf, " +
			itoa(counts["image"]) + " images)",
	}
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

// latestNotebookDBModTime returns the newest modification time across the
// notebook SQLite files so WAL-backed writes still count as source changes.
func latestNotebookDBModTime(dataDir string) time.Time {
	var latest time.Time
	for _, name := range []string{"notebook.db", "notebook.db-wal", "notebook.db-shm"} {
		info, err := os.Stat(filepath.Join(dataDir, name))
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}
