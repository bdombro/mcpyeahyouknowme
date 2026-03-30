package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

	// Check for required environment variables
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		fmt.Fprintln(os.Stderr, "Error: GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET environment variables must be set")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "To set up:")
		fmt.Fprintln(os.Stderr, "1. Go to https://console.cloud.google.com/")
		fmt.Fprintln(os.Stderr, "2. Create a project or select existing one")
		fmt.Fprintln(os.Stderr, "3. Enable Google Docs API and Google Drive API")
		fmt.Fprintln(os.Stderr, "4. Create OAuth 2.0 credentials (Desktop App)")
		fmt.Fprintln(os.Stderr, "5. Set environment variables:")
		fmt.Fprintln(os.Stderr, "   export GOOGLE_CLIENT_ID='your-client-id'")
		fmt.Fprintln(os.Stderr, "   export GOOGLE_CLIENT_SECRET='your-client-secret'")
		os.Exit(1)
	}

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
	errChan := make(chan error)
	server := &http.Server{Addr: ":8085"}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

	// Shutdown server
	server.Shutdown(context.Background())
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

// runGoogleDocsReset removes the OAuth token and clears local data.
func runGoogleDocsReset() {
	fmt.Println("⚠️  This will remove your Google Docs authentication and delete all synced documents.")
	fmt.Print("Are you sure? (yes/no): ")

	var response string
	fmt.Scanln(&response)

	if strings.ToLower(response) != "yes" {
		fmt.Println("Cancelled.")
		return
	}

	// Remove token file
	tokenPath := filepath.Join(dataDir(), "googledocs_token.json")
	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Warning: Failed to remove token: %v\n", err)
	}

	// Remove database
	dbPath := filepath.Join(dataDir(), "googledocs.db")
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Warning: Failed to remove database: %v\n", err)
	}

	fmt.Println("✓ Google Docs data cleared")
	fmt.Println("Run 'mcpyeahyouknowme googledocs login' to re-authenticate")
}
