package gsuite

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
)

// RunLogin handles the single OAuth flow for all Google Workspace apps,
// then prompts the user to select which apps to enable.
func RunLogin(dataDir string) {
	fmt.Println("🔐 Starting Google Suite OAuth login...")
	fmt.Println()

	src := NewSource(dataDir)
	defer src.Close()

	config := src.getOAuthConfig()

	codeVerifier := generateCodeVerifier()
	codeChallenge := generateCodeChallenge(codeVerifier)
	state := generateRandomString(32)

	codeChan := make(chan string)
	errChan := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			errChan <- fmt.Errorf("invalid state parameter")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errorMsg := r.URL.Query().Get("error")
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			errChan <- fmt.Errorf("authorization failed: %s", errorMsg)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Authentication Successful</title></head>
<body style="font-family: sans-serif; text-align: center; padding: 50px;">
<h1>Authentication Successful</h1>
<p>You can close this window and return to the terminal.</p>
</body></html>`)
		codeChan <- code
	})
	srv := &http.Server{Addr: ":8085", Handler: mux}

	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	authURL := config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	fmt.Println("Opening browser for Google authentication...")
	fmt.Println("This will authorize access to: Docs, Sheets, Gmail, Calendar, Tasks, Contacts, Slides")
	fmt.Println()
	fmt.Println("If the browser doesn't open automatically, visit this URL:")
	fmt.Println()
	fmt.Println(authURL)
	fmt.Println()

	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Warning: Failed to open browser automatically: %v\n", err)
	}

	select {
	case code := <-codeChan:
		fmt.Println("Received authorization code, exchanging for token...")

		token, err := config.Exchange(context.Background(), code,
			oauth2.SetAuthURLParam("code_verifier", codeVerifier))
		if err != nil {
			slog.Error("failed to exchange code for token", "err", describeOAuthExchangeError(err))
			srv.Shutdown(context.Background())
			os.Exit(1)
		}

		if err := src.saveToken(token); err != nil {
			slog.Error("failed to save token", "err", err)
			srv.Shutdown(context.Background())
			os.Exit(1)
		}

		if email, err := fetchGoogleEmail(config, token); err == nil && email != "" {
			emailPath := filepath.Join(dataDir, "gsuite_email.txt")
			os.WriteFile(emailPath, []byte(email), 0o600)
		}

		fmt.Println()
		fmt.Println("✓ Successfully authenticated with Google!")
		fmt.Println()

		apps := promptAppSelection()

		authData, _ := json.Marshal(struct {
			Apps AppsConfig `json:"apps"`
		}{Apps: apps})
		if err := core.UpdateSourceConfig(dataDir, "gsuite", func(sc *core.SourceConfig) {
			sc.Enabled = true
			sc.Reset = false
			sc.Auth = authData
		}); err != nil {
			slog.Warn("could not update config.json", "err", err)
		}

		fmt.Println()
		fmt.Println("✓ Configuration saved")
		fmt.Println()
		fmt.Println("You can now:")
		fmt.Println("  • See the status: mcpyeahyouknowme status")
		fmt.Println("  • Use MCP server: mcpyeahyouknowme mcp")
		fmt.Println("  • Toggle apps:    mcpyeahyouknowme gsuite apps")

	case err := <-errChan:
		slog.Error("authentication error", "err", err)
		srv.Shutdown(context.Background())
		os.Exit(1)

	case <-time.After(3 * time.Minute):
		slog.Error("timeout waiting for authentication")
		srv.Shutdown(context.Background())
		os.Exit(1)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}

// RunEnable enables the Google Suite source or a specific app without requiring re-login.
// args[0] may be an app name (docs, sheets, gmail, calendar, tasks, contacts, slides) or "all".
// With "all", every app is enabled and the source is set enabled. With a specific app, only that app is enabled
// and the source is set enabled. With no args, the source is set enabled (app states unchanged).
func RunEnable(dataDir string, args []string) {
	src := NewSource(dataDir)
	defer src.Close()

	if len(args) == 0 || strings.EqualFold(args[0], "all") {
		for _, app := range allApps {
			src.apps.SetEnabled(app.name, true)
		}
		if err := src.saveAppsConfig(src.apps); err != nil {
			// nocov
			fmt.Fprintf(os.Stderr, "Error: could not save gsuite apps config: %v\n", err)
			os.Exit(1)
		}
		if err := core.SetSourceEnabled(dataDir, "gsuite", true); err != nil {
			// nocov
			fmt.Fprintf(os.Stderr, "Error: could not enable gsuite: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("gsuite: enabled (all apps)")
		return
	}

	appName := strings.ToLower(args[0])
	if !isValidAppName(appName) {
		fmt.Fprintf(os.Stderr, "Error: unknown app %q; valid: all, docs, sheets, gmail, calendar, tasks, contacts, slides\n", args[0])
		os.Exit(1)
	}
	src.apps.SetEnabled(appName, true)
	if err := src.saveAppsConfig(src.apps); err != nil {
		// nocov
		fmt.Fprintf(os.Stderr, "Error: could not save gsuite apps config: %v\n", err)
		os.Exit(1)
	}
	if err := core.SetSourceEnabled(dataDir, "gsuite", true); err != nil {
		// nocov
		fmt.Fprintf(os.Stderr, "Error: could not enable gsuite: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("gsuite: enabled (%s)\n", appName)
}

// RunDisable disables the Google Suite source or a specific app.
// With "all" or no args, the source is fully disabled. With a specific app name, only that app is disabled.
func RunDisable(dataDir string, args []string) {
	src := NewSource(dataDir)
	defer src.Close()

	if len(args) == 0 || strings.EqualFold(args[0], "all") {
		if err := core.SetSourceEnabled(dataDir, "gsuite", false); err != nil {
			// nocov
			fmt.Fprintf(os.Stderr, "Error: could not disable gsuite: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("gsuite: disabled")
		return
	}

	appName := strings.ToLower(args[0])
	if !isValidAppName(appName) {
		fmt.Fprintf(os.Stderr, "Error: unknown app %q; valid: all, docs, sheets, gmail, calendar, tasks, contacts, slides\n", args[0])
		os.Exit(1)
	}
	src.apps.SetEnabled(appName, false)
	if err := src.saveAppsConfig(src.apps); err != nil {
		// nocov
		fmt.Fprintf(os.Stderr, "Error: could not save gsuite apps config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("gsuite: disabled (%s)\n", appName)
}

// isValidAppName reports whether name matches a known gsuite app.
func isValidAppName(name string) bool {
	for _, app := range allApps {
		if app.name == name {
			return true
		}
	}
	return false
}

// promptAppSelection asks the user which apps to enable after login.
func promptAppSelection() AppsConfig {
	apps := DefaultAppsConfig()

	fmt.Println("Which Google apps would you like to enable?")
	fmt.Println("All apps start disabled. Enter numbers to enable, `all` to enable everything, or press Enter to keep all disabled.")
	fmt.Println()
	for i, app := range allApps {
		fmt.Printf("  %d. %s\n", i+1, cliAppName(app))
	}
	fmt.Println()
	fmt.Print("Numbers to enable (comma-separated), `all`, or Enter for none: ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			if strings.EqualFold(line, "all") || line == "*" {
				enableAllApps(&apps)
				return apps
			}
			for _, part := range strings.Split(line, ",") {
				part = strings.TrimSpace(part)
				if strings.EqualFold(part, "all") || part == "*" {
					enableAllApps(&apps)
					return apps
				}
				idx := 0
				if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 1 && idx <= len(allApps) {
					apps.SetEnabled(allApps[idx-1].name, true)
					fmt.Printf("  ✓ Enabled %s\n", cliAppName(allApps[idx-1]))
				}
			}
		}
	}

	return apps
}

// enableAllApps flips every app on and prints each enabled label for the interactive CLI flow.
func enableAllApps(apps *AppsConfig) {
	for _, app := range allApps {
		apps.SetEnabled(app.name, true)
		fmt.Printf("  ✓ Enabled %s\n", cliAppName(app))
	}
}

// RunApps shows current per-app status and persists interactive toggles back into config.json, clearing disabled app data on the way down.
func RunApps(dataDir string) {
	src := NewSource(dataDir)
	defer src.Close()

	fmt.Println("Google Suite apps:")
	fmt.Println()
	for i, app := range allApps {
		status := "✓ enabled"
		if !src.apps.IsEnabled(app.name) {
			status = "✗ disabled"
		}
		fmt.Printf("  %d. %s — %s\n", i+1, cliAppName(app), status)
	}
	fmt.Println()
	fmt.Print("Enter numbers to toggle (comma-separated), `all` to enable all, or Enter to keep: ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			return
		}
		if strings.EqualFold(line, "all") || line == "*" {
			enableAllApps(&src.apps)
		} else {
			for _, part := range strings.Split(line, ",") {
				part = strings.TrimSpace(part)
				if strings.EqualFold(part, "all") || part == "*" {
					enableAllApps(&src.apps)
					break
				}
				idx := 0
				if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 1 && idx <= len(allApps) {
					appName := allApps[idx-1].name
					newState := !src.apps.IsEnabled(appName)
					src.apps.SetEnabled(appName, newState)
					if newState {
						fmt.Printf("  ✓ Enabled %s\n", cliAppName(allApps[idx-1]))
					} else {
						fmt.Printf("  ✗ Disabled %s — clearing data...\n", cliAppName(allApps[idx-1]))
						src.ResetApp(appName)
					}
				}
			}
		}
		if err := src.saveAppsConfig(src.apps); err != nil {
			slog.Warn("could not save config", "err", err)
		}
	}
}

// RunReset removes Google Suite data after prompting for confirmation.
func RunReset(dataDir string) {
	fmt.Println("⚠️  This will remove your Google Suite authentication and delete all synced data.")
	fmt.Print("Are you sure? (yes/no): ")

	var response string
	fmt.Scanln(&response)

	if strings.ToLower(response) != "yes" {
		fmt.Println("Cancelled.")
		return
	}

	src := NewSource(dataDir)
	if err := src.Reset(dataDir); err != nil {
		slog.Warn("warning during reset", "err", err)
	}
	if err := core.SetSourceDisabled(dataDir, "gsuite"); err != nil {
		slog.Warn("could not update config.json", "err", err)
	}
	if err := core.ClearSearchSource(dataDir, "gsuite"); err != nil {
		slog.Warn("could not clear search index", "err", err)
	}

	fmt.Println("Google Suite data reset complete")
	fmt.Println("Run 'mcpyeahyouknowme gsuite login' to re-authenticate")
}

// SessionAuthDisplay returns the cached Google account email for status when a token exists, "signed in" when the token exists but no email file is present, or "no" when not signed in.
func SessionAuthDisplay(dataDir string) string {
	if !IsLoggedIn(dataDir) {
		return "no"
	}
	if b, err := os.ReadFile(filepath.Join(dataDir, "gsuite_email.txt")); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return "signed in"
}

// InfoLines returns indented lines for the `info` command Google Suite section.
func InfoLines(dataDir string) []string {
	var lines []string
	sc := core.LoadConfig(dataDir).Sources["gsuite"]
	tokenPath := filepath.Join(dataDir, "gsuite_token.json")
	if sc.Enabled {
		if _, err := os.Stat(tokenPath); err != nil {
			return []string{"   Hint:       run 'mcpyeahyouknowme gsuite login'"}
		}
	} else {
		return nil
	}
	if dbSize := core.FileGroupSizeBytes(filepath.Join(dataDir, "gsuite.db")); dbSize > 0 {
		lines = append(lines, fmt.Sprintf("   Database:   %s", core.FormatSizeMB(dbSize)))
	}

	src := NewSource(dataDir)
	defer src.Close()

	appSizes := map[string]int64{}
	if src.db != nil {
		for _, app := range allApps {
			size, err := core.SQLiteObjectSizeBytes(src.db, app.tablesToDrop)
			if err == nil && size > 0 {
				appSizes[app.name] = size
			}
		}
	}
	for _, app := range allApps {
		if !src.apps.IsEnabled(app.name) {
			lines = append(lines, fmt.Sprintf("   %-11s disabled", cliAppName(app)+":"))
			continue
		}
		if src.db == nil {
			lines = append(lines, fmt.Sprintf("   %-11s no database yet", cliAppName(app)+":"))
			continue
		}
		count, err := app.countRows(src.db)
		if err != nil {
			lines = append(lines, fmt.Sprintf("   %-11s unable to count", cliAppName(app)+":"))
			continue
		}
		syncStatus := src.getSyncStatus(app.name)
		lastSync := src.getLastSyncTime(app.name)
		statusStr := formatSyncStatus(syncStatus, lastSync, count)
		if size, ok := appSizes[app.name]; ok && size > 0 {
			statusStr = fmt.Sprintf("%s — %s", statusStr, core.FormatSizeMB(size))
		}
		lines = append(lines, fmt.Sprintf("   %-11s %s", cliAppName(app)+":", statusStr))
	}
	return lines
}

// cliAppName returns the shorter CLI-facing app label by trimming the shared Google prefix.
func cliAppName(app *appDef) string {
	return strings.TrimPrefix(app.displayName, "Google ")
}

// formatSyncStatus renders one app sync summary from sync state, last sync time, and row count.
func formatSyncStatus(syncStatus string, lastSync time.Time, count int) string {
	if strings.HasPrefix(syncStatus, "syncing") {
		parts := strings.SplitN(syncStatus, ":", 2)
		if len(parts) == 2 {
			return fmt.Sprintf("%d synced — syncing (%s fetched)", count, parts[1])
		}
		return fmt.Sprintf("%d synced — syncing", count)
	}
	if lastSync.IsZero() {
		return fmt.Sprintf("%d synced", count)
	}
	return fmt.Sprintf("%d synced", count)
}

// generateCodeVerifier returns a PKCE verifier for the desktop OAuth flow.
func generateCodeVerifier() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
}

// generateCodeChallenge hashes a PKCE verifier into the S256 challenge Google expects.
func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])
}

// generateRandomString returns a URL-safe random string for OAuth state and similar one-time values.
func generateRandomString(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)[:length]
}

// openBrowser asks macOS to open url in the default browser so login can continue outside the terminal.
func openBrowser(url string) error {
	return exec.Command("open", url).Start()
}

// fetchGoogleEmail reads the authenticated account email so info output can show which account is linked.
func fetchGoogleEmail(config *oauth2.Config, token *oauth2.Token) (string, error) {
	client := config.Client(context.Background(), token)
	resp, err := client.Get("https://www.googleapis.com/drive/v3/about?fields=user(emailAddress)")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		User struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.User.EmailAddress, nil
}
