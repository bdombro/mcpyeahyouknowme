package browser_history

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Verifies MCP tool returns configuration guidance when browser_history is not enabled/configured.
func TestMCP_Search_unconfigured(t *testing.T) {
	dataDir := t.TempDir()
	src := NewSource(dataDir)
	server := buildMCPServer(t, src)

	text, isErr := callTool(t, server, "browser_history_search", map[string]interface{}{"query": "x"})
	if !isErr {
		t.Fatal("expected tool error")
	}
	if text == "" {
		t.Fatal("expected error text")
	}
}

// Verifies MCP tool validates sort input before querying the snapshot.
func TestMCP_Search_invalidSort(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "chrome", true)
	src := NewSource(dataDir)
	server := buildMCPServer(t, src)

	_, isErr := callTool(t, server, "browser_history_search", map[string]interface{}{
		"sort": "sideways",
	})
	if !isErr {
		t.Fatal("expected sort validation error")
	}
}

// Verifies MCP search requires a query argument.
func TestMCP_Search_requiresQuery(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "chrome", true)
	src := NewSource(dataDir)
	server := buildMCPServer(t, src)

	_, isErr := callTool(t, server, "browser_history_search", map[string]interface{}{})
	if !isErr {
		t.Fatal("expected required query error")
	}
}

// Verifies MCP tool surfaces a clear error when config exists but daemon snapshot is missing.
func TestMCP_Search_missingSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	saveTestConfig(t, dataDir, "chrome", true)
	src := NewSource(dataDir)
	server := buildMCPServer(t, src)

	_, isErr := callTool(t, server, "browser_history_search", map[string]interface{}{
		"query": "github",
	})
	if !isErr {
		t.Fatal("expected missing snapshot error")
	}
}

// Verifies MCP tool returns rows from the current snapshot without triggering a refresh.
func TestMCP_Search_success(t *testing.T) {
	dataDir := t.TempDir()
	snapshotPath := filepath.Join(dataDir, "browser_history.db")
	db := newHistoryDB(t, snapshotPath)
	insertVisit(t, db, 1, 21, "https://github.com", "GitHub", chromeEpochOffsetMicros+9_000_000, 1)
	saveTestConfig(t, dataDir, "chrome", true)

	src := NewSource(dataDir)
	server := buildMCPServer(t, src)

	text, isErr := callTool(t, server, "browser_history_search", map[string]interface{}{
		"query": "github",
		"sort":  "recent",
		"limit": 5.0,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var rows []VisitRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("unmarshal tool result: %v text=%s", err, text)
	}
	if len(rows) != 1 || rows[0].VisitID != 21 {
		t.Fatalf("rows = %+v", rows)
	}
}

// Verifies MCP list returns rows without requiring query filtering.
func TestMCP_List_success(t *testing.T) {
	dataDir := t.TempDir()
	snapshotPath := filepath.Join(dataDir, "browser_history.db")
	db := newHistoryDB(t, snapshotPath)
	insertVisit(t, db, 1, 22, "https://example.com", "Example", chromeEpochOffsetMicros+10_000_000, 1)
	saveTestConfig(t, dataDir, "chrome", true)

	src := NewSource(dataDir)
	server := buildMCPServer(t, src)

	text, isErr := callTool(t, server, "browser_history_list", map[string]interface{}{
		"sort": "recent",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var rows []VisitRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("unmarshal tool result: %v text=%s", err, text)
	}
	if len(rows) != 1 || rows[0].VisitID != 22 {
		t.Fatalf("rows = %+v", rows)
	}
}

// Verifies MCP list validates sort input and ignores non-string sort values by falling back to the default.
func TestMCP_List_sortHandling(t *testing.T) {
	t.Run("invalid sort", func(t *testing.T) {
		dataDir := t.TempDir()
		saveTestConfig(t, dataDir, "chrome", true)
		src := NewSource(dataDir)
		server := buildMCPServer(t, src)

		_, isErr := callTool(t, server, "browser_history_list", map[string]interface{}{"sort": "sideways"})
		if !isErr {
			t.Fatal("expected invalid sort error")
		}
	})

	t.Run("non-string sort falls back", func(t *testing.T) {
		dataDir := t.TempDir()
		snapshotPath := filepath.Join(dataDir, "browser_history.db")
		db := newHistoryDB(t, snapshotPath)
		insertVisit(t, db, 1, 22, "https://example.com", "Example", chromeEpochOffsetMicros+10_000_000, 1)
		saveTestConfig(t, dataDir, "chrome", true)

		src := NewSource(dataDir)
		server := buildMCPServer(t, src)
		text, isErr := callTool(t, server, "browser_history_list", map[string]interface{}{"sort": 123})
		if isErr {
			t.Fatalf("unexpected error: %s", text)
		}
	})
}

// Verifies MCP tools surface underlying DB query failures from malformed snapshots.
func TestMCP_Search_queryError(t *testing.T) {
	dataDir := t.TempDir()
	snapshotPath := filepath.Join(dataDir, "browser_history.db")
	db, err := sql.Open("sqlite", snapshotPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE dummy (id INTEGER PRIMARY KEY);`); err != nil {
		t.Fatalf("create dummy table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	saveTestConfig(t, dataDir, "chrome", true)

	src := NewSource(dataDir)
	server := buildMCPServer(t, src)
	_, isErr := callTool(t, server, "browser_history_search", map[string]interface{}{"query": "github"})
	if !isErr {
		t.Fatal("expected query error")
	}
}
