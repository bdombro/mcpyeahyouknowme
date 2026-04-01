package gsuite

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
)

func TestStartCore_invalidGrantResetsAndDisables(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gsuite_token.json"), []byte(`{"refresh_token":"x"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	originalApps := allApps
	allApps = []*appDef{{
		name:        "docs",
		displayName: "Google Docs",
		initSchema:  func(*sql.DB) error { return nil },
		syncFunc: func(syncContext) error {
			return &oauth2.RetrieveError{
				Response:  &http.Response{StatusCode: http.StatusBadRequest},
				ErrorCode: "invalid_grant",
				Body:      []byte(`{"error":"invalid_grant"}`),
			}
		},
	}}
	t.Cleanup(func() { allApps = originalApps })

	src := &Source{
		dataDir: dir,
		token:   &oauth2.Token{RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)},
		apps:    DefaultAppsConfig(),
	}
	src.apps.SetEnabled("docs", true)
	if err := src.saveAppsConfig(src.apps); err != nil {
		t.Fatalf("saveAppsConfig: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := src.StartCore(ctx); err != nil {
		t.Fatalf("StartCore: %v", err)
	}

	if core.LoadConfig(dir).Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite to be disabled after invalid_grant")
	}
	if _, err := os.Stat(filepath.Join(dir, "gsuite_token.json")); !os.IsNotExist(err) {
		t.Fatal("expected token to be deleted after invalid_grant reset")
	}
}

func TestSyncAllApps_http401DoesNotDisableSource(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "gsuite", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}

	originalApps := allApps
	allApps = []*appDef{{
		name:        "docs",
		displayName: "Google Docs",
		initSchema:  func(*sql.DB) error { return nil },
		syncFunc: func(syncContext) error {
			return &googleapi.Error{Code: http.StatusUnauthorized, Message: "unauthorized"}
		},
	}}
	t.Cleanup(func() { allApps = originalApps })

	src := &Source{
		dataDir: dir,
		token:   &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)},
		apps:    DefaultAppsConfig(),
	}
	src.apps.SetEnabled("docs", true)

	if err := src.syncAllApps(context.Background()); err != nil {
		t.Fatalf("syncAllApps: %v", err)
	}
	if !core.LoadConfig(dir).Sources["gsuite"].Enabled {
		t.Fatal("expected gsuite to remain enabled after HTTP 401")
	}
}

func TestSyncAllApps_reloadsAppsConfigFromDisk(t *testing.T) {
	dir := t.TempDir()

	originalApps := allApps
	called := false
	allApps = []*appDef{{
		name:        "docs",
		displayName: "Google Docs",
		initSchema:  func(*sql.DB) error { return nil },
		syncFunc: func(syncContext) error {
			called = true
			return nil
		},
	}}
	t.Cleanup(func() { allApps = originalApps })

	cfgWriter := &Source{dataDir: dir}
	apps := DefaultAppsConfig()
	apps.SetEnabled("docs", true)
	if err := cfgWriter.saveAppsConfig(apps); err != nil {
		t.Fatalf("saveAppsConfig: %v", err)
	}

	src := &Source{
		dataDir: dir,
		token:   &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)},
		apps:    DefaultAppsConfig(),
	}

	if err := src.syncAllApps(context.Background()); err != nil {
		t.Fatalf("syncAllApps: %v", err)
	}
	if !called {
		t.Fatal("expected sync func to run after reloading apps config")
	}
	if !src.apps.IsEnabled("docs") {
		t.Fatal("expected in-memory apps config to be refreshed from disk")
	}
}
