package gsuite

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
)

// GoogleClientID and GoogleClientSecret are injected at build time via ldflags.
var GoogleClientID string
var GoogleClientSecret string

// IsConfigured reports whether the binary was built with the required Google OAuth credentials.
func IsConfigured() bool {
	return GoogleClientID != "" && GoogleClientSecret != ""
}

type gsuiteErrKind string

const (
	gsuiteErrOther        gsuiteErrKind = "other"
	gsuiteErrInvalidGrant gsuiteErrKind = "invalid_grant"
	gsuiteErrUnauthorized gsuiteErrKind = "401"
	gsuiteErrForbidden    gsuiteErrKind = "403"
)

// All scopes needed across all Google Workspace apps.
var oauthScopes = []string{
	"https://www.googleapis.com/auth/documents.readonly",
	"https://www.googleapis.com/auth/spreadsheets.readonly",
	"https://www.googleapis.com/auth/drive.readonly",
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/calendar.readonly",
	"https://www.googleapis.com/auth/tasks.readonly",
	"https://www.googleapis.com/auth/contacts.readonly",
	"https://www.googleapis.com/auth/presentations.readonly",
}

// getOAuthConfig builds the shared OAuth client config every GSuite login, refresh, and live API path uses.
func (g *Source) getOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     GoogleClientID,
		ClientSecret: GoogleClientSecret,
		RedirectURL:  "http://127.0.0.1:8085",
		Scopes:       oauthScopes,
		Endpoint:     google.Endpoint,
	}
}

// describeOAuthExchangeError rewrites known token-exchange failures into actionable rebuild/login guidance.
func describeOAuthExchangeError(err error) string {
	if err == nil {
		return ""
	}

	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		body := strings.ToLower(string(retrieveErr.Body))
		code := strings.ToLower(retrieveErr.ErrorCode)
		if code == "invalid_request" && strings.Contains(body, "client_secret is missing") {
			return "Google rejected the token exchange because `GOOGLE_CLIENT_SECRET` is missing from the built binary. Set both `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` from the same Google Cloud OAuth client, rebuild/reinstall the binary, and run `mcpyeahyouknowme gsuite login` again."
		}
	}

	return err.Error()
}

// classifyGSuiteError maps OAuth/API failures into daemon retry buckets so core can disable or retry appropriately.
func classifyGSuiteError(err error) gsuiteErrKind {
	if err == nil {
		return gsuiteErrOther
	}

	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		body := strings.ToLower(string(retrieveErr.Body))
		code := strings.ToLower(retrieveErr.ErrorCode)
		if code == "invalid_grant" || strings.Contains(body, "invalid_grant") {
			return gsuiteErrInvalidGrant
		}
		if retrieveErr.Response != nil {
			switch retrieveErr.Response.StatusCode {
			case 401:
				return gsuiteErrUnauthorized
			case 403:
				return gsuiteErrForbidden
			}
		}
	}

	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case 401:
			return gsuiteErrUnauthorized
		case 403:
			return gsuiteErrForbidden
		}
		if strings.Contains(strings.ToLower(apiErr.Message), "invalid_grant") {
			return gsuiteErrInvalidGrant
		}
	}

	msg := ""
	if retrieveErr != nil && retrieveErr.Response == nil {
		msg = strings.ToLower(string(retrieveErr.Body))
	} else {
		msg = strings.ToLower(err.Error())
	}
	switch {
	case strings.Contains(msg, "invalid_grant"):
		return gsuiteErrInvalidGrant
	case strings.Contains(msg, "401"):
		return gsuiteErrUnauthorized
	case strings.Contains(msg, "403"):
		return gsuiteErrForbidden
	default:
		return gsuiteErrOther
	}
}

// loadToken reads the persisted OAuth token from disk into g.token so daemon sync and live API calls can authenticate.
func (g *Source) loadToken() error {
	tokenPath := filepath.Join(g.dataDir, "gsuite_token.json")
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

// saveToken updates g.token and persists the refreshed token to disk so later daemon polls and MCP calls reuse it.
func (g *Source) saveToken(token *oauth2.Token) error {
	g.token = token
	tokenPath := filepath.Join(g.dataDir, "gsuite_token.json")
	data, err := json.Marshal(token)
	if err != nil { // nocov — oauth2.Token is always marshallable
		return err
	}
	return os.WriteFile(tokenPath, data, 0600)
}

// isAuthenticated applies the daemon's auth gate: g.token must exist, and expired access tokens are only acceptable when a refresh token exists.
func (g *Source) isAuthenticated() bool {
	if g.token == nil {
		return false
	}
	if g.token.RefreshToken == "" && g.token.Expiry.Before(time.Now()) {
		return false
	}
	return true
}
