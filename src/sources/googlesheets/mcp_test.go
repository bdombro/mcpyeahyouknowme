package googlesheets

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// TestGoogleSheetsMCP_RegisterTools verifies tools are registered without panic.
func TestGoogleSheetsMCP_RegisterTools(t *testing.T) {
	g := newMCPTestSource(t)
	s := server.NewMCPServer("test", "0.0.1")
	g.RegisterTools(s)
}

// ---------- handleSearch ----------

func TestGoogleSheetsMCP_HandleSearch_NilDB(t *testing.T) {
	g := &Source{db: nil}
	text, isErr := callHandler(t, g.handleSearch, map[string]interface{}{"query": "anything"})
	if isErr {
		t.Error("nil-db search should return empty result, not error")
	}
	_ = text
}

func TestGoogleSheetsMCP_HandleSearch_Success(t *testing.T) {
	g := newMCPTestSource(t)
	text, isErr := callHandler(t, g.handleSearch, map[string]interface{}{"query": "alpha"})
	if isErr {
		t.Fatalf("unexpected tool error")
	}
	if !strings.Contains(text, "sheet1") {
		t.Errorf("expected sheet1 in results, got: %s", text)
	}
}

func TestGoogleSheetsMCP_HandleSearch_DBError(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db}
	db.Close()
	_, isErr := callHandler(t, g.handleSearch, map[string]interface{}{"query": "anything"})
	if !isErr {
		t.Error("expected error result when db is closed")
	}
}

// ---------- handleGetSpreadsheet ----------

func TestGoogleSheetsMCP_HandleGetSpreadsheet(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*testing.T) *Source
		sheetID string
		wantErr bool
		want    string
	}{
		{
			name:    "nil db",
			setup:   func(t *testing.T) *Source { return &Source{db: nil} },
			sheetID: "sheet1",
			wantErr: true,
		},
		{
			name:    "not found",
			setup:   newMCPTestSource,
			sheetID: "no-such-sheet",
			wantErr: true,
		},
		{
			name:    "success",
			setup:   newMCPTestSource,
			sheetID: "sheet1",
			wantErr: false,
			want:    "Alpha Spreadsheet",
		},
		{
			name: "db error",
			setup: func(t *testing.T) *Source {
				db := newTestGoogleSheetsDB(t)
				g := &Source{db: db}
				db.Close()
				return g
			},
			sheetID: "sheet1",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup(t)
			result, err := g.handleGetSpreadsheet(context.Background(), buildMCPRequest(map[string]interface{}{"spreadsheet_id": tt.sheetID}))
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

func TestGoogleSheetsMCP_HandleListRecent_NilDB(t *testing.T) {
	g := &Source{db: nil}
	text, isErr := callHandler(t, g.handleListRecent, map[string]interface{}{})
	if isErr {
		t.Error("nil-db list should return empty result, not error")
	}
	if !strings.Contains(text, "count") {
		t.Errorf("expected count in nil-db result, got: %s", text)
	}
}

func TestGoogleSheetsMCP_HandleListRecent_Success(t *testing.T) {
	g := newMCPTestSource(t)
	text, isErr := callHandler(t, g.handleListRecent, map[string]interface{}{})
	if isErr {
		t.Fatalf("unexpected tool error")
	}
	if !strings.Contains(text, "sheet2") {
		t.Errorf("expected sheet2 in list, got: %s", text)
	}
}

func TestGoogleSheetsMCP_HandleListRecent_DBError(t *testing.T) {
	db := newTestGoogleSheetsDB(t)
	g := &Source{db: db}
	db.Close()
	_, isErr := callHandler(t, g.handleListRecent, map[string]interface{}{})
	if !isErr {
		t.Error("expected error result when db is closed")
	}
}
