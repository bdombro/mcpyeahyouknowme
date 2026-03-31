package googledocs

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// TestGoogleDocsMCP_RegisterTools verifies tools are registered without panic.
func TestGoogleDocsMCP_RegisterTools(t *testing.T) {
	g := newMCPTestSource(t)
	s := server.NewMCPServer("test", "0.0.1")
	g.RegisterTools(s)
}

// ---------- handleSearch ----------

func TestGoogleDocsMCP_HandleSearch_NilDB(t *testing.T) {
	g := &Source{db: nil}
	text, isErr := callHandler(t, g.handleSearch, map[string]interface{}{"query": "anything"})
	if isErr {
		t.Error("nil-db search should return empty result, not error")
	}
	_ = text
}

func TestGoogleDocsMCP_HandleSearch_Success(t *testing.T) {
	g := newMCPTestSource(t)
	text, isErr := callHandler(t, g.handleSearch, map[string]interface{}{"query": "alpha"})
	if isErr {
		t.Fatalf("unexpected tool error")
	}
	if !strings.Contains(text, "doc1") {
		t.Errorf("expected doc1 in results, got: %s", text)
	}
}

func TestGoogleDocsMCP_HandleSearch_DBError(t *testing.T) {
	db := newTestGoogleDocsDB(t)
	g := &Source{db: db}
	db.Close()
	_, isErr := callHandler(t, g.handleSearch, map[string]interface{}{"query": "anything"})
	if !isErr {
		t.Error("expected error result when db is closed")
	}
}

// ---------- handleGetDocument ----------

func TestGoogleDocsMCP_HandleGetDocument(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*testing.T) *Source
		docID   string
		wantErr bool
		want    string
	}{
		{
			name:    "nil db",
			setup:   func(t *testing.T) *Source { return &Source{db: nil} },
			docID:   "doc1",
			wantErr: true,
		},
		{
			name:    "not found",
			setup:   newMCPTestSource,
			docID:   "no-such-doc",
			wantErr: true,
		},
		{
			name:    "success",
			setup:   newMCPTestSource,
			docID:   "doc1",
			wantErr: false,
			want:    "Alpha Document",
		},
		{
			name: "db error",
			setup: func(t *testing.T) *Source {
				db := newTestGoogleDocsDB(t)
				g := &Source{db: db}
				db.Close()
				return g
			},
			docID:   "doc1",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup(t)
			result, err := g.handleGetDocument(context.Background(), buildMCPRequest(map[string]interface{}{"document_id": tt.docID}))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError != tt.wantErr {
				t.Errorf("IsError=%v, want %v", result.IsError, tt.wantErr)
			}
			if tt.want != "" {
				text := extractResultText(t, result)
				if !strings.Contains(text, tt.want) {
					t.Errorf("expected %q in result, got: %s", tt.want, text)
				}
			}
		})
	}
}

// ---------- handleListRecent ----------

func TestGoogleDocsMCP_HandleListRecent_NilDB(t *testing.T) {
	g := &Source{db: nil}
	text, isErr := callHandler(t, g.handleListRecent, map[string]interface{}{})
	if isErr {
		t.Error("nil-db list should return empty result, not error")
	}
	if !strings.Contains(text, "count") {
		t.Errorf("expected count in nil-db result, got: %s", text)
	}
}

func TestGoogleDocsMCP_HandleListRecent_Success(t *testing.T) {
	g := newMCPTestSource(t)
	text, isErr := callHandler(t, g.handleListRecent, map[string]interface{}{})
	if isErr {
		t.Fatalf("unexpected tool error")
	}
	if !strings.Contains(text, "doc2") {
		t.Errorf("expected doc2 in list, got: %s", text)
	}
}

func TestGoogleDocsMCP_HandleListRecent_DBError(t *testing.T) {
	db := newTestGoogleDocsDB(t)
	g := &Source{db: db}
	db.Close()
	_, isErr := callHandler(t, g.handleListRecent, map[string]interface{}{})
	if !isErr {
		t.Error("expected error result when db is closed")
	}
}
