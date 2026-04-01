package googlesheets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/sheets/v4"
)

// GoogleClientID and GoogleClientSecret are injected at build time via ldflags.
var (
	GoogleClientID     string
	GoogleClientSecret string
)

// getOAuthConfig returns the OAuth2 config for Google Sheets access.
func (g *Source) getOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     GoogleClientID,
		ClientSecret: GoogleClientSecret,
		RedirectURL:  "http://127.0.0.1:8086",
		Scopes: []string{
			sheets.SpreadsheetsReadonlyScope,
			drive.DriveReadonlyScope,
		},
		Endpoint: google.Endpoint,
	}
}

// loadToken loads the OAuth token from disk.
func (g *Source) loadToken() error {
	tokenPath := filepath.Join(g.dataDir, "googlesheets_token.json")
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return err
	}
	g.token = &token
	return nil
}

// saveToken saves the OAuth token to disk.
func (g *Source) saveToken(token *oauth2.Token) error {
	g.token = token
	tokenPath := filepath.Join(g.dataDir, "googlesheets_token.json")
	data, err := json.Marshal(token)
	if err != nil { // nocov — oauth2.Token is always marshallable
		return err
	}
	return os.WriteFile(tokenPath, data, 0600)
}

// isAuthenticated checks if we have a valid (or refreshable) token.
func (g *Source) isAuthenticated() bool {
	if g.token == nil {
		return false
	}
	if g.token.RefreshToken == "" && g.token.Expiry.Before(time.Now()) {
		return false
	}
	return true
}
