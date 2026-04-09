package gsuite

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
)

// init registers the gsuite source name so config normalization keeps a stable entry for it.
func init() {
	core.RegisterKnownSource("gsuite")
}

// appDef describes a single Google Workspace app within the gsuite source.
type appDef struct {
	name          string
	displayName   string
	initSchema    func(*sql.DB) error
	syncFunc      func(ctx syncContext) error
	registerTools func(src *Source, prefix string, s core.ToolAdder)
	searchEntries func(db *sql.DB, sourceName string) ([]core.SearchEntry, error)
	streamEntries func(db *sql.DB, sourceName string, emit func([]core.SearchEntry) error) error
	countRows     func(*sql.DB) (int, error)
	tablesToDrop  []string
}

// syncContext bundles everything an app sync function needs.
type syncContext struct {
	Ctx        interface{ Done() <-chan struct{} } // context.Context without import cycle
	HTTPClient *http.Client
	DB         *sql.DB
	SelfEmail  string
	SetStatus  func(status string)
}

// allApps defines every Google Workspace app this source knows about.
// Order determines display and registration order.
var allApps = []*appDef{
	docsAppDef,
	sheetsAppDef,
	gmailAppDef,
	calendarAppDef,
	tasksAppDef,
	contactsAppDef,
	slidesAppDef,
}

// AppsConfig tracks which apps are enabled.
type AppsConfig struct {
	Docs     bool `json:"docs"`
	Sheets   bool `json:"sheets"`
	Gmail    bool `json:"gmail"`
	Calendar bool `json:"calendar"`
	Tasks    bool `json:"tasks"`
	Contacts bool `json:"contacts"`
	Slides   bool `json:"slides"`
}

// DefaultAppsConfig starts every Google app disabled so login or CLI selection must opt sources into sync and MCP exposure.
func DefaultAppsConfig() AppsConfig {
	return AppsConfig{}
}

// IsEnabled answers whether appName is enabled so sync, search, and tool registration can gate per-app behavior.
func (ac AppsConfig) IsEnabled(appName string) bool {
	switch appName {
	case "docs":
		return ac.Docs
	case "sheets":
		return ac.Sheets
	case "gmail":
		return ac.Gmail
	case "calendar":
		return ac.Calendar
	case "tasks":
		return ac.Tasks
	case "contacts":
		return ac.Contacts
	case "slides":
		return ac.Slides
	default:
		return false
	}
}

// SetEnabled flips one app flag in-place so login and app toggles can persist the desired sync/tool surface.
func (ac *AppsConfig) SetEnabled(appName string, enabled bool) {
	switch appName {
	case "docs":
		ac.Docs = enabled
	case "sheets":
		ac.Sheets = enabled
	case "gmail":
		ac.Gmail = enabled
	case "calendar":
		ac.Calendar = enabled
	case "tasks":
		ac.Tasks = enabled
	case "contacts":
		ac.Contacts = enabled
	case "slides":
		ac.Slides = enabled
	}
}

// Source implements core.DataSource and core.CoreService for Google Workspace.
type Source struct {
	db      *sql.DB
	token   *oauth2.Token
	dataDir string
	apps    AppsConfig
}

// NewSource opens the unified GSuite DB and cached auth/app config from dataDir, tolerating DB-open failure so status and config reads can still work.
func NewSource(dataDir string) *Source {
	db, err := openGSuiteDB(dataDir)
	if err != nil {
		db = nil
	}
	src := &Source{db: db, dataDir: dataDir}
	src.loadToken()
	src.apps = src.loadAppsConfig()
	return src
}

// IsLoggedIn reports whether a token file exists so CLI and registry code can gate auth-required flows cheaply.
func IsLoggedIn(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "gsuite_token.json"))
	return err == nil
}

// Name returns the source key used for config, registry lookup, and tool prefixes.
func (g *Source) Name() string { return "gsuite" }

// Description returns the human label shown in CLI and status output.
func (g *Source) Description() string { return "Google Suite" }

// Close releases the gsuite database handle so callers do not leak SQLite connections.
func (g *Source) Close() error {
	if g.db != nil {
		return g.db.Close()
	}
	return nil
}

// Reset removes all Google Suite data files.
func (g *Source) Reset(dataDir string) error {
	return core.DefaultReset(dataDir, []string{
		"gsuite.db",
		"gsuite.db-wal",
		"gsuite.db-shm",
		"gsuite_token.json",
		"gsuite_email.txt",
	})
}

// ResetApp removes only a specific app's data tables (not auth or other apps).
func (g *Source) ResetApp(appName string) error {
	if g.db == nil {
		return nil
	}
	for _, app := range allApps {
		if app.name == appName {
			for _, table := range app.tablesToDrop {
				g.db.Exec("DROP TABLE IF EXISTS " + table)
			}
			g.db.Exec("DELETE FROM sync_state WHERE key LIKE ?", appName+"_%")
			return app.initSchema(g.db)
		}
	}
	return nil
}

// SearchEntries gathers indexable rows from enabled apps for global search, skipping per-app extraction failures rather than failing the whole source.
func (g *Source) SearchEntries() ([]core.SearchEntry, error) {
	var all []core.SearchEntry
	err := g.StreamSearchEntries(func(entries []core.SearchEntry) error {
		all = append(all, entries...)
		return nil
	})
	return all, err
}

// StreamSearchEntries emits one app at a time so daemon indexing avoids
// holding every Google Workspace app's entries in one combined slice.
// Errors originating from the emit callback (e.g. context cancellation) are
// propagated immediately; errors from app-internal logic (e.g. DB failures in
// one app) are logged and skipped so the remaining apps are still indexed.
func (g *Source) StreamSearchEntries(emit func([]core.SearchEntry) error) error {
	if g.db == nil || emit == nil {
		return nil
	}
	var emitErr error
	wrappedEmit := func(batch []core.SearchEntry) error {
		err := emit(batch)
		if err != nil {
			emitErr = err
		}
		return err
	}
	for _, app := range allApps {
		if !g.apps.IsEnabled(app.name) {
			continue
		}
		if app.streamEntries != nil {
			if err := app.streamEntries(g.db, g.Name(), wrappedEmit); err != nil {
				if emitErr != nil {
					return emitErr
				}
				continue
			}
			continue
		}
		entries, err := app.searchEntries(g.db, g.Name())
		if err != nil || len(entries) == 0 {
			continue
		}
		if err := wrappedEmit(entries); err != nil {
			return err
		}
	}
	return nil
}

// HasChangesSince checks the local GSuite SQLite files so incremental daemon
// ticks can skip a full re-export when the synced cache did not change.
func (g *Source) HasChangesSince(t time.Time) bool {
	if t.IsZero() {
		return true
	}
	latest := latestGSuiteDBModTime(g.dataDir)
	if latest.IsZero() {
		return true
	}
	return !latest.Before(t)
}

// loadAppsConfig reads per-app enablement from config.json so daemon polls and CLI toggles share one persisted source of truth.
func (g *Source) loadAppsConfig() AppsConfig {
	cfg := core.LoadConfig(g.dataDir)
	sc := cfg.Sources["gsuite"]
	if len(sc.Auth) == 0 {
		return DefaultAppsConfig()
	}
	var wrapper struct {
		Apps AppsConfig `json:"apps"`
	}
	if err := json.Unmarshal(sc.Auth, &wrapper); err != nil {
		return DefaultAppsConfig()
	}
	return wrapper.Apps
}

// saveAppsConfig persists per-app enablement back into the gsuite source auth blob and updates the in-memory copy as a side effect.
func (g *Source) saveAppsConfig(apps AppsConfig) error {
	g.apps = apps
	authData, _ := json.Marshal(struct {
		Apps AppsConfig `json:"apps"`
	}{Apps: apps})
	return core.UpdateSourceConfig(g.dataDir, "gsuite", func(sc *core.SourceConfig) {
		sc.Auth = authData
	})
}

// AppDefs returns the known app definitions (for use by CLI/info).
//
//revive:disable-next-line:unexported-return
func AppDefs() []*appDef {
	return allApps
}

// latestGSuiteDBModTime returns the newest modification time across the
// GSuite SQLite files so WAL-backed writes still count as source changes.
func latestGSuiteDBModTime(dataDir string) time.Time {
	var latest time.Time
	for _, name := range []string{"gsuite.db", "gsuite.db-wal", "gsuite.db-shm"} {
		info, err := os.Stat(filepath.Join(dataDir, name))
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}
