package gsuite

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
			fmt.Fprintf(os.Stderr, "Error: Failed to exchange code for token: %v\n", err)
			srv.Shutdown(context.Background())
			os.Exit(1)
		}

		if err := src.saveToken(token); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save token: %v\n", err)
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

		cfg := core.LoadConfig(dataDir)
		authData, _ := json.Marshal(struct {
			Apps AppsConfig `json:"apps"`
		}{Apps: apps})
		cfg.Sources["gsuite"] = core.SourceConfig{Enabled: true, Auth: authData}
		if err := core.SaveConfig(dataDir, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update config.json: %v\n", err)
		}

		fmt.Println()
		fmt.Println("✓ Configuration saved")
		fmt.Println()
		fmt.Println("You can now:")
		fmt.Println("  • See the status: mcpyeahyouknowme info")
		fmt.Println("  • Use MCP server: mcpyeahyouknowme mcp")
		fmt.Println("  • Toggle apps:    mcpyeahyouknowme gsuite apps")

	case err := <-errChan:
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		srv.Shutdown(context.Background())
		os.Exit(1)

	case <-time.After(3 * time.Minute):
		fmt.Fprintln(os.Stderr, "Error: Timeout waiting for authentication")
		srv.Shutdown(context.Background())
		os.Exit(1)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}

// promptAppSelection asks the user which apps to enable after login.
func promptAppSelection() AppsConfig {
	apps := DefaultAppsConfig()

	fmt.Println("Which Google apps would you like to enable?")
	fmt.Println("All are enabled by default. Enter numbers to disable, or press Enter to keep all.")
	fmt.Println()
	for i, app := range allApps {
		fmt.Printf("  %d. %s\n", i+1, app.displayName)
	}
	fmt.Println()
	fmt.Print("Numbers to disable (comma-separated), or Enter for all: ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			for _, part := range strings.Split(line, ",") {
				part = strings.TrimSpace(part)
				idx := 0
				if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 1 && idx <= len(allApps) {
					apps.SetEnabled(allApps[idx-1].name, false)
					fmt.Printf("  ✗ Disabled %s\n", allApps[idx-1].displayName)
				}
			}
		}
	}

	return apps
}

// RunApps shows current app status and allows toggling.
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
		fmt.Printf("  %d. %s — %s\n", i+1, app.displayName, status)
	}
	fmt.Println()
	fmt.Print("Enter numbers to toggle (comma-separated), or Enter to keep: ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			return
		}
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			idx := 0
			if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 1 && idx <= len(allApps) {
				appName := allApps[idx-1].name
				newState := !src.apps.IsEnabled(appName)
				src.apps.SetEnabled(appName, newState)
				if newState {
					fmt.Printf("  ✓ Enabled %s\n", allApps[idx-1].displayName)
				} else {
					fmt.Printf("  ✗ Disabled %s — clearing data...\n", allApps[idx-1].displayName)
					src.ResetApp(appName)
				}
			}
		}
		if err := src.saveAppsConfig(src.apps); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Warning during reset: %v\n", err)
	}

	fmt.Println("Google Suite data reset complete")
	fmt.Println("Run 'mcpyeahyouknowme gsuite login' to re-authenticate")
}

// InfoLines returns indented lines for the `info` command Google Suite section.
func InfoLines(dataDir string) []string {
	var lines []string
	tokenPath := filepath.Join(dataDir, "gsuite_token.json")
	if _, err := os.Stat(tokenPath); err != nil {
		lines = append(lines, "   Logged in:  no (run 'mcpyeahyouknowme gsuite login')")
		return lines
	}
	if email, err := os.ReadFile(filepath.Join(dataDir, "gsuite_email.txt")); err == nil && len(email) > 0 {
		lines = append(lines, fmt.Sprintf("   Logged in:  %s", string(email)))
	} else {
		lines = append(lines, "   Logged in:  yes")
	}

	src := NewSource(dataDir)
	defer src.Close()

	for _, app := range allApps {
		if !src.apps.IsEnabled(app.name) {
			lines = append(lines, fmt.Sprintf("   %-12s disabled", app.displayName+":"))
			continue
		}
		if src.db == nil {
			lines = append(lines, fmt.Sprintf("   %-12s no database yet", app.displayName+":"))
			continue
		}
		count, err := app.countRows(src.db)
		if err != nil {
			lines = append(lines, fmt.Sprintf("   %-12s unable to count", app.displayName+":"))
			continue
		}
		syncStatus := src.getSyncStatus(app.name)
		lastSync := src.getLastSyncTime(app.name)
		statusStr := formatSyncStatus(syncStatus, lastSync, count)
		lines = append(lines, fmt.Sprintf("   %-12s %s", app.displayName+":", statusStr))
	}
	return lines
}

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
	ago := time.Since(lastSync).Truncate(time.Second)
	return fmt.Sprintf("%d synced — idle (last sync: %s ago)", count, ago)
}

func generateCodeVerifier() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
}

func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])
}

func generateRandomString(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)[:length]
}

func openBrowser(url string) error {
	return exec.Command("open", url).Start()
}

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
