package main

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

	"golang.org/x/oauth2"
)

// runGoogleDocsLogin handles the OAuth loopback flow.
func runGoogleDocsLogin() {
	fmt.Println("🔐 Starting Google Docs OAuth login...")
	fmt.Println()

	// Create Google Docs source to get OAuth config
	src, err := NewGoogleDocsSource()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to initialize: %v\n", err)
		os.Exit(1)
	}
	defer src.Close()

	config := src.getOAuthConfig()

	// Generate PKCE code verifier and challenge
	codeVerifier := generateCodeVerifier()
	codeChallenge := generateCodeChallenge(codeVerifier)

	// Create OAuth state for CSRF protection
	state := generateRandomString(32)

	// Start local HTTP server
	codeChan := make(chan string)
	errChan := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Ignore non-root requests (e.g. /favicon.ico) so they don't block on channels
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		// Check state to prevent CSRF
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

		// Success page
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
			<!DOCTYPE html>
			<html>
			<head><title>Authentication Successful</title></head>
			<body style="font-family: sans-serif; text-align: center; padding: 50px;">
			<h1>Authentication Successful</h1>
				<p>You can close this window and return to the terminal.</p>
			</body>
			</html>
		`)

		codeChan <- code
	})
	server := &http.Server{Addr: ":8085", Handler: mux}

	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Build authorization URL with PKCE
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

	// Open browser
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Warning: Failed to open browser automatically: %v\n", err)
	}

	// Wait for callback or timeout
	select {
	case code := <-codeChan:
		// Exchange authorization code for token
		fmt.Println("Received authorization code, exchanging for token...")

		token, err := config.Exchange(context.Background(), code,
			oauth2.SetAuthURLParam("code_verifier", codeVerifier))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to exchange code for token: %v\n", err)
			server.Shutdown(context.Background())
			os.Exit(1)
		}

		// Save token
		if err := src.saveToken(token); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save token: %v\n", err)
			server.Shutdown(context.Background())
			os.Exit(1)
		}

		// Fetch and save account email for the info command
		if email, err := fetchGoogleEmail(config, token); err == nil && email != "" {
			emailPath := filepath.Join(dataDir(), "googledocs_email.txt")
			os.WriteFile(emailPath, []byte(email), 0o600)
		}

		fmt.Println()
		fmt.Println("✓ Successfully authenticated with Google!")
		fmt.Println("✓ Token saved for daemon use")
		fmt.Println()
		fmt.Println("You can now:")
		fmt.Println("  • Start the daemon: mcpyeahyouknowme start")
		fmt.Println("  • Use MCP server: mcpyeahyouknowme mcp")

	case err := <-errChan:
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		server.Shutdown(context.Background())
		os.Exit(1)

	case <-time.After(3 * time.Minute):
		fmt.Fprintln(os.Stderr, "Error: Timeout waiting for authentication")
		server.Shutdown(context.Background())
		os.Exit(1)
	}

	// Shutdown server with a timeout in case connections linger
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}

// generateCodeVerifier generates a random code verifier for PKCE.
func generateCodeVerifier() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
}

// generateCodeChallenge generates the code challenge from the verifier.
func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])
}

// generateRandomString generates a random string for state parameter.
func generateRandomString(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)[:length]
}

// openBrowser opens a URL in the default browser (macOS only).
func openBrowser(url string) error {
	cmd := exec.Command("open", url)
	return cmd.Start()
}

// fetchGoogleEmail retrieves the authenticated user's email via Drive API.
// Uses Drive's About endpoint which is covered by the existing DriveReadonly scope.
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

// runGoogleDocsReset prompts for confirmation then delegates to googleDocsReset.
func runGoogleDocsReset() {
	fmt.Println("⚠️  This will remove your Google Docs authentication and delete all synced documents.")
	fmt.Print("Are you sure? (yes/no): ")

	var response string
	fmt.Scanln(&response)

	if strings.ToLower(response) != "yes" {
		fmt.Println("Cancelled.")
		return
	}

	googleDocsReset(dataDir(), plistPath())
}

// googleDocsReset removes Google Docs data while preserving the daemon
// installation and other data sources. Accepts paths so tests can use temp dirs.
func googleDocsReset(dDir, plist string) {
	if _, err := os.Stat(dDir); os.IsNotExist(err) {
		fmt.Println("Nothing to reset (data directory does not exist)")
		return
	}

	daemonInstalled := false
	if _, err := os.Stat(plist); err == nil {
		daemonInstalled = true
		exec.Command("launchctl", "unload", plist).Run()
		fmt.Println("Stopped core daemon")
	}

	googleDocsFiles := []string{
		filepath.Join(dDir, "googledocs_token.json"),
		filepath.Join(dDir, "googledocs_email.txt"),
		filepath.Join(dDir, "googledocs.db"),
	}

	for _, file := range googleDocsFiles {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", file, err)
		} else if err == nil {
			fmt.Printf("Removed %s\n", file)
		}
	}

	if daemonInstalled {
		exec.Command("launchctl", "load", plist).Run()
		fmt.Println("Restarted core daemon (Google Docs disabled)")
	}

	fmt.Println("Google Docs data reset complete")
	fmt.Println("Run 'mcpyeahyouknowme googledocs login' to re-authenticate")
}
