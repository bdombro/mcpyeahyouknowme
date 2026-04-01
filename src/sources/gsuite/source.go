package gsuite

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/oauth2"
)

// toolAdder abstracts server.MCPServer.AddTool for testing.
type toolAdder interface {
	AddTool(tool mcp.Tool, handler server.ToolHandlerFunc)
}

// appDef describes a single Google Workspace app within the gsuite source.
type appDef struct {
	name          string
	displayName   string
	initSchema    func(*sql.DB) error
	syncFunc      func(ctx syncContext) error
	registerTools func(src *Source, prefix string, s toolAdder)
	searchEntries func(db *sql.DB, sourceName string) ([]core.SearchEntry, error)
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

// DefaultAppsConfig returns config with all apps enabled.
func DefaultAppsConfig() AppsConfig {
	return AppsConfig{
		Docs: true, Sheets: true, Gmail: true,
		Calendar: true, Tasks: true, Contacts: true, Slides: true,
	}
}

// IsEnabled returns whether a specific app is enabled.
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

// SetEnabled sets a specific app's enabled state.
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

// NewSource creates a new Google Workspace source rooted at dataDir.
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

// IsLoggedIn returns true if a Google OAuth token exists.
func IsLoggedIn(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "gsuite_token.json"))
	return err == nil
}

func (g *Source) Name() string        { return "gsuite" }
func (g *Source) Description() string { return "Google Suite" }
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

// SearchEntries returns all indexable content from all enabled apps.
func (g *Source) SearchEntries() ([]core.SearchEntry, error) {
	if g.db == nil {
		return nil, nil
	}
	var all []core.SearchEntry
	for _, app := range allApps {
		if !g.apps.IsEnabled(app.name) {
			continue
		}
		entries, err := app.searchEntries(g.db, g.Name())
		if err != nil {
			continue
		}
		all = append(all, entries...)
	}
	return all, nil
}

// loadAppsConfig reads the per-app config from config.json.
func (g *Source) loadAppsConfig() AppsConfig {
	cfg := core.LoadConfig(g.dataDir)
	sc, ok := cfg.Sources["gsuite"]
	if !ok || sc.Auth == nil {
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

// saveAppsConfig writes the per-app config to config.json.
func (g *Source) saveAppsConfig(apps AppsConfig) error {
	g.apps = apps
	cfg := core.LoadConfig(g.dataDir)
	authData, _ := json.Marshal(struct {
		Apps AppsConfig `json:"apps"`
	}{Apps: apps})
	sc := cfg.Sources["gsuite"]
	sc.Auth = authData
	sc.Enabled = true
	cfg.Sources["gsuite"] = sc
	return core.SaveConfig(g.dataDir, cfg)
}

// AppDefs returns the known app definitions (for use by CLI/info).
func AppDefs() []*appDef {
	return allApps
}
