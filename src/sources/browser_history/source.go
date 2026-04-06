package browser_history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
)

var updateSourceConfig = core.UpdateSourceConfig

// Registers the browser_history source so config normalization always keeps a stable slot.
func init() {
	core.RegisterKnownSource("browser_history")
}

// BrowserHistoryConfig stores the selected browser whose local History DB should be indexed.
type BrowserHistoryConfig struct {
	Browser string `json:"browser"`
}

// Source implements core.DataSource for local browser history snapshots.
type Source struct {
	dataDir      string
	snapshotPath string

	resolvePath    func(browser string) (string, error)
	copySnapshot   func(sourcePath, destPath string) (sourceFileMeta, error)
	openSnapshotDB func(snapshotPath string) (*sql.DB, error)

	fallbackRefresh time.Duration

	mu             sync.Mutex
	lastBrowser    string
	lastSourceMeta sourceFileMeta
	lastRefreshed  time.Time
}

// IsConfigured reports whether browser_history has a valid browser selection.
func IsConfigured(dataDir string) bool {
	cfg := loadBrowserHistoryConfig(dataDir)
	return normalizeBrowser(cfg.Browser) != ""
}

// Loads browser_history config from source auth bytes in config.json.
func loadBrowserHistoryConfig(dataDir string) BrowserHistoryConfig {
	sc := core.LoadConfig(dataDir).Sources["browser_history"]
	if len(sc.Auth) == 0 {
		return BrowserHistoryConfig{}
	}
	var cfg BrowserHistoryConfig
	if err := json.Unmarshal(sc.Auth, &cfg); err != nil {
		return BrowserHistoryConfig{}
	}
	cfg.Browser = normalizeBrowser(cfg.Browser)
	return cfg
}

// Saves browser_history config into config.json so daemon and MCP use the same browser selection.
func saveBrowserHistoryConfig(dataDir string, cfg BrowserHistoryConfig) error {
	cfg.Browser = normalizeBrowser(cfg.Browser)
	data, err := json.Marshal(cfg)
	if err != nil { // nocov
		return err
	}
	return updateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Auth = data
	})
}

// Name returns the source key used for registry lookup and tool prefixes.
func (s *Source) Name() string { return "browser_history" }

// Description returns the human label shown in CLI and info output.
func (s *Source) Description() string { return "Browser History" }

// Close is a no-op because this source keeps no long-lived DB handle.
func (s *Source) Close() error { return nil }

// Reset removes local snapshot files and leaves browser-owned history files untouched.
func (s *Source) Reset(dataDir string) error {
	return core.DefaultReset(dataDir, []string{
		"browser_history.db",
		"browser_history.db-wal",
		"browser_history.db-shm",
	})
}

// RegisterTools adds browser history read tools to the MCP server.
func (s *Source) RegisterTools(srv *server.MCPServer) {
	registerTools(s, srv)
}

// SearchEntries refreshes the local snapshot when needed and returns per-url entries for global indexing.
func (s *Source) SearchEntries() ([]core.SearchEntry, error) {
	cfg := loadBrowserHistoryConfig(s.dataDir)
	if normalizeBrowser(cfg.Browser) == "" {
		return nil, nil
	}
	if err := s.refreshSnapshotIfNeeded(cfg.Browser); err != nil {
		return nil, err
	}

	db, err := s.openSnapshotDB(s.snapshotPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := listIndexRows(db)
	if err != nil {
		return nil, err
	}
	return buildSearchEntries(rows), nil
}

// HasChangesSince checks the local browser-history snapshot files so
// incremental daemon ticks can skip re-reading them when they are unchanged.
func (s *Source) HasChangesSince(t time.Time) bool {
	if t.IsZero() {
		return true
	}
	latest := latestBrowserSnapshotModTime(s.snapshotPath)
	if latest.IsZero() {
		return true
	}
	return !latest.Before(t)
}

// InfoLines returns source status lines for the `info` command output.
func InfoLines(dataDir string) []string {
	sc := core.LoadConfig(dataDir).Sources["browser_history"]
	if !sc.Enabled {
		return []string{"   Status:     disabled"}
	}
	cfg := loadBrowserHistoryConfig(dataDir)
	if normalizeBrowser(cfg.Browser) == "" {
		return []string{
			"   Status:     enabled (browser not configured)",
			"   Hint:       run 'mcpyeahyouknowme browser_history enable <chrome|brave>'",
		}
	}
	lines := []string{
		"   Status:     enabled",
		fmt.Sprintf("   Browser:    %s", cfg.Browser),
	}
	size := core.FileGroupSizeBytes(filepath.Join(dataDir, "browser_history.db"))
	if size > 0 {
		lines = append(lines, "   Snapshot:   "+core.FormatSizeMB(size))
	}
	return lines
}

// Opens the existing local snapshot for MCP reads without triggering a refresh operation.
func (s *Source) openSnapshotForRead() (*sql.DB, error) {
	if _, err := os.Stat(s.snapshotPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("browser history snapshot not available yet; start the daemon to index history first")
		}
		return nil, err
	}
	return s.openSnapshotDB(s.snapshotPath)
}

// Refreshes the local snapshot when source metadata changed or fallback interval elapsed.
func (s *Source) refreshSnapshotIfNeeded(browser string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	resolvedBrowser := normalizeBrowser(browser)
	sourcePath, err := s.resolvePath(resolvedBrowser)
	if err != nil {
		return err
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}

	changed := s.lastBrowser != resolvedBrowser ||
		s.lastSourceMeta.Size != info.Size() ||
		!s.lastSourceMeta.ModifiedAt.Equal(info.ModTime())
	stale := s.lastRefreshed.IsZero() || time.Since(s.lastRefreshed) >= s.fallbackRefresh
	if !changed && !stale {
		return nil
	}

	meta, err := s.copySnapshot(sourcePath, s.snapshotPath)
	if err != nil {
		return err
	}
	s.lastBrowser = resolvedBrowser
	s.lastSourceMeta = meta
	s.lastRefreshed = time.Now().UTC()
	return nil
}

// latestBrowserSnapshotModTime returns the newest modification time across the
// copied browser-history snapshot files so WAL-backed writes count as changes.
func latestBrowserSnapshotModTime(snapshotPath string) time.Time {
	var latest time.Time
	for _, path := range []string{snapshotPath, snapshotPath + "-wal", snapshotPath + "-shm"} {
		info, err := os.Stat(path)
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}
