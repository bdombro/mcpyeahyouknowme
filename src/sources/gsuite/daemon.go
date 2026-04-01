package gsuite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
)

// RequiresAuth returns true because Google Suite needs OAuth.
func (g *Source) RequiresAuth() bool { return true }

// StartCore runs the periodic sync for all enabled Google Workspace apps.
func (g *Source) StartCore(ctx context.Context) error { // nocov
	fmt.Println("Starting Google Suite sync daemon...")
	if !g.isAuthenticated() { // nocov
		return fmt.Errorf("not authenticated - run 'mcpyeahyouknowme gsuite login' first")
	}
	return core.RunPollLoop(ctx, 5*time.Minute, func(pollCtx context.Context) error {
		return g.syncAllApps(pollCtx)
	})
}

func (g *Source) syncAllApps(ctx context.Context) error { // nocov
	if g.db == nil { // nocov
		var err error
		g.db, err = openGSuiteDB(g.dataDir)
		if err != nil { // nocov
			return fmt.Errorf("cannot open database: %w", err)
		}
	}

	oauthConfig := g.getOAuthConfig()
	syncToken := *g.token
	if syncToken.Expiry.IsZero() {
		syncToken.Expiry = time.Now().Add(-time.Second)
	}
	ts := oauthConfig.TokenSource(ctx, &syncToken)
	httpClient := oauth2.NewClient(ctx, ts)

	selfEmail := ""
	if data, err := os.ReadFile(filepath.Join(g.dataDir, "gsuite_email.txt")); err == nil {
		selfEmail = strings.TrimSpace(string(data))
	}

	for _, app := range allApps {
		if !g.apps.IsEnabled(app.name) {
			continue
		}
		sctx := syncContext{
			Ctx:        ctx,
			HTTPClient: httpClient,
			DB:         g.db,
			SelfEmail:  selfEmail,
			SetStatus: func(status string) {
				g.setSyncStatus(app.name, status)
			},
		}
		if err := app.syncFunc(sctx); err != nil { // nocov
			fmt.Fprintf(os.Stderr, "Warning: %s sync error: %v\n", app.displayName, err)
		}
		g.setLastSyncTime(app.name, time.Now())
	}

	// Persist refreshed token
	if fresh, err := ts.Token(); err == nil {
		if err2 := g.saveToken(fresh); err2 != nil { // nocov
			fmt.Printf("Warning: Failed to persist refreshed token: %v\n", err2)
		}
	}
	return nil
}

func (g *Source) getLastSyncTime(appName string) time.Time {
	if g.db == nil {
		return time.Time{}
	}
	var value string
	err := g.db.QueryRow("SELECT value FROM sync_state WHERE key = ?", appName+"_last_sync").Scan(&value)
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, value)
	return t
}

func (g *Source) setLastSyncTime(appName string, t time.Time) {
	if g.db == nil {
		return
	}
	g.db.Exec(`INSERT OR REPLACE INTO sync_state (key, value) VALUES (?, ?)`,
		appName+"_last_sync", t.Format(time.RFC3339))
}

func (g *Source) getSyncStatus(appName string) string {
	if g.db == nil {
		return ""
	}
	var value string
	g.db.QueryRow("SELECT value FROM sync_state WHERE key = ?", appName+"_sync_status").Scan(&value)
	return value
}

func (g *Source) setSyncStatus(appName, status string) {
	if g.db == nil {
		return
	}
	g.db.Exec(`INSERT OR REPLACE INTO sync_state (key, value) VALUES (?, ?)`,
		appName+"_sync_status", status)
}
