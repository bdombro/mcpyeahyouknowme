package gsuite

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
)

func TestClassifyGSuiteError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want gsuiteErrKind
	}{
		{
			name: "nil error",
			err:  nil,
			want: gsuiteErrOther,
		},
		{
			name: "invalid grant retrieve error",
			err: &oauth2.RetrieveError{
				Response:  &http.Response{StatusCode: http.StatusBadRequest},
				ErrorCode: "invalid_grant",
				Body:      []byte(`{"error":"invalid_grant"}`),
			},
			want: gsuiteErrInvalidGrant,
		},
		{
			name: "google api 401",
			err:  &googleapi.Error{Code: http.StatusUnauthorized, Message: "unauthorized"},
			want: gsuiteErrUnauthorized,
		},
		{
			name: "google api 403",
			err:  &googleapi.Error{Code: http.StatusForbidden, Message: "forbidden"},
			want: gsuiteErrForbidden,
		},
		{
			name: "retrieve error 403",
			err: &oauth2.RetrieveError{
				Response: &http.Response{StatusCode: http.StatusForbidden},
				Body:     []byte(`{"error":"access_denied"}`),
			},
			want: gsuiteErrForbidden,
		},
		{
			name: "retrieve error 401",
			err: &oauth2.RetrieveError{
				Response: &http.Response{StatusCode: http.StatusUnauthorized},
				Body:     []byte(`{"error":"unauthorized"}`),
			},
			want: gsuiteErrUnauthorized,
		},
		{
			name: "retrieve error without response falls through",
			err: &oauth2.RetrieveError{
				Body: []byte(`{"error":"temporarily_unavailable"}`),
			},
			want: gsuiteErrOther,
		},
		{
			name: "google api invalid grant message",
			err:  &googleapi.Error{Code: http.StatusBadRequest, Message: "oauth invalid_grant"},
			want: gsuiteErrInvalidGrant,
		},
		{
			name: "google api other",
			err:  &googleapi.Error{Code: http.StatusBadRequest, Message: "bad request"},
			want: gsuiteErrOther,
		},
		{
			name: "wrapped invalid grant",
			err:  errors.New("oauth token exchange failed: invalid_grant"),
			want: gsuiteErrInvalidGrant,
		},
		{
			name: "string 401",
			err:  errors.New("request failed with 401"),
			want: gsuiteErrUnauthorized,
		},
		{
			name: "string 403",
			err:  errors.New("request failed with 403"),
			want: gsuiteErrForbidden,
		},
		{
			name: "other",
			err:  errors.New("boom"),
			want: gsuiteErrOther,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyGSuiteError(tc.err); got != tc.want {
				t.Fatalf("classifyGSuiteError() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetOAuthConfig_usesClientIDAndLoopbackRedirect(t *testing.T) {
	oldID := GoogleClientID
	GoogleClientID = "client-id"
	defer func() { GoogleClientID = oldID }()

	cfg := (&Source{}).getOAuthConfig()
	if cfg.ClientID != "client-id" {
		t.Fatalf("ClientID = %q, want %q", cfg.ClientID, "client-id")
	}
	if cfg.RedirectURL != "http://127.0.0.1:8085" {
		t.Fatalf("RedirectURL = %q", cfg.RedirectURL)
	}
	if len(cfg.Scopes) != len(oauthScopes) {
		t.Fatalf("Scopes len = %d, want %d", len(cfg.Scopes), len(oauthScopes))
	}
}

func TestLoadToken_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gsuite_token.json"), []byte("{bad json"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	src := &Source{dataDir: dir}
	if err := src.loadToken(); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}
