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

// GoogleClientID is injected at build time via ldflags.
var GoogleClientID string

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

// getOAuthConfig returns the OAuth2 config for all Google Workspace apps.
func (g *Source) getOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:    GoogleClientID,
		RedirectURL: "http://127.0.0.1:8085",
		Scopes:      oauthScopes,
		Endpoint:    google.Endpoint,
	}
}

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

// loadToken loads the OAuth token from disk.
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

// saveToken saves the OAuth token to disk.
func (g *Source) saveToken(token *oauth2.Token) error {
	g.token = token
	tokenPath := filepath.Join(g.dataDir, "gsuite_token.json")
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
