package googlesheets

import (
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

// RunLogin handles the OAuth loopback (PKCE) flow for Google Sheets.
func RunLogin(dataDir string) {
	fmt.Println("🔐 Starting Google Sheets OAuth login...")
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
	srv := &http.Server{Addr: ":8086", Handler: mux}

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
			emailPath := filepath.Join(dataDir, "googlesheets_email.txt")
			os.WriteFile(emailPath, []byte(email), 0o600)
		}

		cfg := core.LoadConfig(dataDir)
		cfg.Sources["googlesheets"] = core.SourceConfig{Enabled: true}
		if err := core.SaveConfig(dataDir, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update config.json: %v\n", err)
		}

		fmt.Println()
		fmt.Println("✓ Successfully authenticated with Google!")
		fmt.Println("✓ Token saved for daemon use")
		fmt.Println()
		fmt.Println("You can now:")
		fmt.Println("  • See the status: mcpyeahyouknowme info")
		fmt.Println("  • Use MCP server: mcpyeahyouknowme mcp")

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

// RunReset removes Google Sheets data after prompting for confirmation.
func RunReset(dataDir string) {
	fmt.Println("⚠️  This will remove your Google Sheets authentication and delete all synced spreadsheets.")
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

	fmt.Println("Google Sheets data reset complete")
	fmt.Println("Run 'mcpyeahyouknowme googlesheets login' to re-authenticate")
}

// InfoLines returns indented lines for the `info` command Google Sheets section.
func InfoLines(dataDir string) []string {
	var lines []string
	tokenPath := filepath.Join(dataDir, "googlesheets_token.json")
	if _, err := os.Stat(tokenPath); err != nil {
		lines = append(lines, "   Logged in:  no (run 'mcpyeahyouknowme googlesheets login')")
	} else if email, err := os.ReadFile(filepath.Join(dataDir, "googlesheets_email.txt")); err == nil && len(email) > 0 {
		lines = append(lines, fmt.Sprintf("   Logged in:  %s", string(email)))
	} else {
		lines = append(lines, "   Logged in:  yes")
	}

	dbPath := filepath.Join(dataDir, "googlesheets.db")
	if _, err := os.Stat(dbPath); err != nil {
		lines = append(lines, "   Spreadsheets:  no database yet")
		return lines
	}

	src := NewSource(dataDir)
	defer src.Close()

	if src.db == nil {
		lines = append(lines, "   Spreadsheets:  unable to read database")
		return lines
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var n int
	if err := src.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM spreadsheets").Scan(&n); err != nil {
		lines = append(lines, "   Spreadsheets:  unable to count rows")
		return lines
	}
	lines = append(lines, fmt.Sprintf("   Spreadsheets:  %d synced", n))
	return lines
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
