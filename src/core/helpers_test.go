package core

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"
)

// Verifies IntArg accepts native int values from MCP-style argument maps.
func TestIntArg_int(t *testing.T) {
	args := map[string]interface{}{"n": 42}
	if got := IntArg(args, "n", 0); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

// Verifies IntArg coerces float64 JSON-style numbers into ints.
func TestIntArg_float64(t *testing.T) {
	args := map[string]interface{}{"n": float64(7)}
	if got := IntArg(args, "n", 0); got != 7 {
		t.Errorf("expected 7, got %d", got)
	}
}

// Verifies IntArg falls back to the provided default when the key is absent.
func TestIntArg_missing(t *testing.T) {
	args := map[string]interface{}{}
	if got := IntArg(args, "n", 99); got != 99 {
		t.Errorf("expected default 99, got %d", got)
	}
}

// Verifies StringArg trims strings and returns empty for missing keys or non-string values.
func TestStringArg(t *testing.T) {
	args := map[string]interface{}{
		"a": "  x  ",
		"b": 42,
	}
	if got := StringArg(args, "a"); got != "x" {
		t.Errorf("a = %q, want x", got)
	}
	if got := StringArg(args, "b"); got != "" {
		t.Errorf("b = %q, want empty", got)
	}
	if got := StringArg(args, "missing"); got != "" {
		t.Errorf("missing = %q, want empty", got)
	}
}

// Verifies JsonResult wraps marshalable values in a non-error MCP text result.
func TestJsonResult_valid(t *testing.T) {
	result, err := JsonResult(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("JsonResult: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// Verifies JsonResult turns marshal failures into MCP error results instead of returning a Go error.
func TestJsonResult_unmarshalable(t *testing.T) {
	// JsonResult converts marshal errors into a tool error result (never returns err).
	ch := make(chan int)
	result, err := JsonResult(ch)
	if err != nil {
		t.Fatalf("unexpected error: JsonResult should not return err, got %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for unmarshalable value")
	}
}

// Verifies ToolDescription appends the example payload when one is supplied.
func TestToolDescription_withExample(t *testing.T) {
	got := ToolDescription("Search contacts.", `{"query":"alice"}`)
	want := `Search contacts. Example arguments: {"query":"alice"}`
	if got != want {
		t.Fatalf("ToolDescription() = %q, want %q", got, want)
	}
}

// Verifies ToolDescription returns the bare summary unchanged when no example is supplied.
func TestToolDescription_withoutExample(t *testing.T) {
	got := ToolDescription("Search contacts.", "")
	if got != "Search contacts." {
		t.Fatalf("ToolDescription() = %q", got)
	}
}

// Verifies NewReadOnlyTool stamps the MCP annotations clients rely on for safe read-only tools.
func TestNewReadOnlyTool_setsAnnotations(t *testing.T) {
	tool := NewReadOnlyTool("search", "Search data.")
	if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
		t.Fatal("expected read-only hint to be true")
	}
	if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
		t.Fatal("expected destructive hint to be false")
	}
	if tool.Annotations.IdempotentHint == nil || !*tool.Annotations.IdempotentHint {
		t.Fatal("expected idempotent hint to be true")
	}
}

// Verifies NewMutatingTool stamps the MCP annotations clients rely on for state-changing tools.
func TestNewMutatingTool_setsAnnotations(t *testing.T) {
	tool := NewMutatingTool("send_message", "Send message.")
	if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint {
		t.Fatal("expected read-only hint to be false")
	}
	if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
		t.Fatal("expected destructive hint to be true")
	}
	if tool.Annotations.IdempotentHint == nil || *tool.Annotations.IdempotentHint {
		t.Fatal("expected idempotent hint to be false")
	}
}

// Verifies missing required string args become retryable MCP error results with example guidance.
func TestRequireStringArgument_missing(t *testing.T) {
	value, result := RequireStringArgument(mcp.CallToolRequest{}, "query", `{"query":"family dinner"}`)
	if value != "" {
		t.Fatalf("expected empty value, got %q", value)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected MCP error result")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `query parameter is required`) {
		t.Fatalf("expected missing parameter text, got %s", text)
	}
	if !strings.Contains(text, `retry with params.arguments`) {
		t.Fatalf("expected retry hint to mention params.arguments, got %s", text)
	}
	if !strings.Contains(text, `{\"query\":\"family dinner\"}`) {
		t.Fatalf("expected example arguments in error, got %s", text)
	}
}

// Verifies missing required string args still return a retryable MCP error when no example payload is available.
func TestRequireStringArgument_missingWithoutExample(t *testing.T) {
	_, result := RequireStringArgument(mcp.CallToolRequest{}, "query", "")
	if result == nil || !result.IsError {
		t.Fatal("expected MCP error result")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(data), `query parameter is required`) {
		t.Fatalf("expected missing parameter text, got %s", string(data))
	}
}

// Verifies RequireStringArgument returns the provided value without an error result on valid input.
func TestRequireStringArgument_success(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "family dinner"}
	value, result := RequireStringArgument(req, "query", `{"query":"family dinner"}`)
	if value != "family dinner" {
		t.Fatalf("expected argument value, got %q", value)
	}
	if result != nil {
		t.Fatal("expected nil error result")
	}
}

// Verifies RequireNumberArgument accepts numeric args and returns no MCP error on valid input.
func TestRequireNumberArgument_success(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"limit": float64(7)}
	value, result := RequireNumberArgument(req, "limit", `{"limit":7}`)
	if value != 7 {
		t.Fatalf("expected numeric argument value, got %v", value)
	}
	if result != nil {
		t.Fatal("expected nil error result")
	}
}

// Verifies missing required number args become retryable MCP error results with example guidance.
func TestRequireNumberArgument_missing(t *testing.T) {
	_, result := RequireNumberArgument(mcp.CallToolRequest{}, "limit", `{"limit":7}`)
	if result == nil || !result.IsError {
		t.Fatal("expected MCP error result")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `limit parameter is required`) {
		t.Fatalf("expected missing parameter text, got %s", text)
	}
	if !strings.Contains(text, `retry with params.arguments`) {
		t.Fatalf("expected retry hint to mention params.arguments, got %s", text)
	}
	if !strings.Contains(text, `{\"limit\":7}`) {
		t.Fatalf("expected example arguments in error, got %s", text)
	}
}

// Verifies RequireIntArgument coerces numeric input into an int for handler use.
func TestRequireIntArgument_success(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"days": float64(14)}
	value, result := RequireIntArgument(req, "days", `{"days":14}`)
	if value != 14 {
		t.Fatalf("expected integer argument value, got %d", value)
	}
	if result != nil {
		t.Fatal("expected nil error result")
	}
}

// Verifies ClearSearchSource removes only one source's rows from search.db and leaves other indexed rows intact.
func TestClearSearchSource_deletesMatchingRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE search_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			source_id TEXT NOT NULL,
			content_type TEXT NOT NULL,
			title TEXT,
			content TEXT NOT NULL,
			metadata TEXT,
			timestamp DATETIME,
			UNIQUE(source, source_id, content_type)
		);
		INSERT INTO search_entries (source, source_id, content_type, title, content)
		VALUES
			('gsuite', 'thread-1', 'email_thread_subject', 'John Thomas', 'John Thomas has 3 kids'),
			('notebook', 'note-1', 'note_title', 'John Thomas', 'John Thomas');
	`); err != nil {
		t.Fatalf("seed search db: %v", err)
	}

	if err := ClearSearchSource(dir, "gsuite"); err != nil {
		t.Fatalf("ClearSearchSource: %v", err)
	}

	var gsuiteCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'gsuite'`).Scan(&gsuiteCount); err != nil {
		t.Fatalf("count gsuite rows: %v", err)
	}
	if gsuiteCount != 0 {
		t.Fatalf("expected gsuite rows to be deleted, got %d", gsuiteCount)
	}
	var notebookCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = 'notebook'`).Scan(&notebookCount); err != nil {
		t.Fatalf("count notebook rows: %v", err)
	}
	if notebookCount != 1 {
		t.Fatalf("expected notebook row to remain, got %d", notebookCount)
	}
}

// Verifies ClearSearchSource is a no-op when the shared search index file has not been created yet.
func TestClearSearchSource_missingFile(t *testing.T) {
	dir := t.TempDir()
	if err := ClearSearchSource(dir, "gsuite"); err != nil {
		t.Fatalf("ClearSearchSource: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "search.db")); !os.IsNotExist(err) {
		t.Fatalf("expected search.db to remain absent, stat err = %v", err)
	}
}

// Verifies ClearSearchSource tolerates malformed or schema-less DBs so resets still finish after partial cleanup.
func TestClearSearchSource_missingTableAndStatError(t *testing.T) {
	t.Run("missing table", func(t *testing.T) {
		dir := t.TempDir()
		db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "search.db")+"?_pragma=foreign_keys(on)")
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		if _, err := db.Exec(`CREATE TABLE placeholder (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatalf("create placeholder: %v", err)
		}

		if err := ClearSearchSource(dir, "gsuite"); err != nil {
			t.Fatalf("ClearSearchSource: %v", err)
		}
	})

	t.Run("stat error", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "not-a-directory")
		if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		if err := ClearSearchSource(filePath, "gsuite"); err == nil {
			t.Fatal("expected stat error for file-backed data dir")
		}
	})
}

// Verifies RequireBoolArgument accepts boolean input and returns no MCP error on valid input.
func TestRequireBoolArgument_success(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"include_raw": true}
	value, result := RequireBoolArgument(req, "include_raw", `{"include_raw":true}`)
	if !value {
		t.Fatal("expected bool argument value to be true")
	}
	if result != nil {
		t.Fatal("expected nil error result")
	}
}

// Verifies missing required bool args become retryable MCP error results with example guidance.
func TestRequireBoolArgument_missing(t *testing.T) {
	_, result := RequireBoolArgument(mcp.CallToolRequest{}, "include_raw", `{"include_raw":true}`)
	if result == nil || !result.IsError {
		t.Fatal("expected MCP error result")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `include_raw parameter is required`) {
		t.Fatalf("expected missing parameter text, got %s", text)
	}
	if !strings.Contains(text, `retry with params.arguments`) {
		t.Fatalf("expected retry hint to mention params.arguments, got %s", text)
	}
	if !strings.Contains(text, `{\"include_raw\":true}`) {
		t.Fatalf("expected example arguments in error, got %s", text)
	}
}

// Verifies the low-value-content heuristic skips numeric-heavy text but keeps prose-like content indexable.
func TestIsLowValueContent(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "numeric grid",
			text: strings.Repeat("$1,234.56\t2024-01-15\tINV-00123\t", 4),
			want: true,
		},
		{
			name: "mixed prose and numbers",
			text: strings.Repeat("Revenue Q1: $1.2M and pipeline expansion plan. ", 3),
			want: false,
		},
		{
			name: "short content never skipped",
			text: "2024-01-15\t12345\t67890",
			want: false,
		},
		{
			name: "unicode letters count as prose",
			text: strings.Repeat("Metrica senor Q1 cafe numero 123. ", 3),
			want: false,
		},
		{
			name: "dates and ids only",
			text: strings.Repeat("2024-01-15 INV-00123 555-1200 ", 4),
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLowValueContent(tc.text); got != tc.want {
				t.Fatalf("IsLowValueContent() = %v, want %v for %q", got, tc.want, tc.text)
			}
		})
	}
}

// Verifies NormalizeConfig seeds config entries for known sources instead of omitting them.
func TestNormalizeConfig_seedsKnownSources(t *testing.T) {
	RegisterKnownSource("core_test_seeded")
	cfg := NormalizeConfig(Config{})
	if _, ok := cfg.Sources["core_test_seeded"]; !ok {
		t.Fatal("expected known source to be seeded")
	}
}

// Verifies disabling a source preserves its config entry while clearing enabled and reset flags.
func TestSetSourceDisabled_preservesEntry(t *testing.T) {
	RegisterKnownSource("core_test_toggle")
	dir := t.TempDir()
	if err := SetSourceEnabled(dir, "core_test_toggle", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	if err := SetSourceDisabled(dir, "core_test_toggle"); err != nil {
		t.Fatalf("SetSourceDisabled: %v", err)
	}

	cfg := LoadConfig(dir)
	sc, ok := cfg.Sources["core_test_toggle"]
	if !ok {
		t.Fatal("expected source entry to remain after disable")
	}
	if sc.Enabled {
		t.Fatal("expected source to be disabled")
	}
	if sc.Reset {
		t.Fatal("expected reset flag to be cleared")
	}
	if got := ConfigPath(dir); got != filepath.Join(dir, "config.json") {
		t.Fatalf("ConfigPath() = %q", got)
	}
}
