package browser_history

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"testing"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
	_ "modernc.org/sqlite"
)

// Builds a throwaway sqlite history DB with Chrome-like urls/visits schema for isolated tests.
func newHistoryDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE urls (
			id INTEGER PRIMARY KEY,
			url TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			visit_count INTEGER NOT NULL DEFAULT 0,
			last_visit_time INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE visits (
			id INTEGER PRIMARY KEY,
			url INTEGER NOT NULL,
			visit_time INTEGER NOT NULL
		);`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Inserts one url row and one visit row for compact fixture setup in tests.
func insertVisit(t *testing.T, db *sql.DB, urlID, visitID int64, url, title string, visitMicros int64, visitCount int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO urls (id, url, title, visit_count, last_visit_time) VALUES (?, ?, ?, ?, ?)`,
		urlID, url, title, visitCount, visitMicros,
	); err != nil {
		t.Fatalf("insert url: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO visits (id, url, visit_time) VALUES (?, ?, ?)`,
		visitID, urlID, visitMicros,
	); err != nil {
		t.Fatalf("insert visit: %v", err)
	}
}

// Writes browser_history auth config into config.json for test setup.
func saveTestConfig(t *testing.T, dataDir, browser string, enabled bool) {
	t.Helper()
	cfg := BrowserHistoryConfig{Browser: browser}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := core.UpdateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Enabled = enabled
		sc.Reset = false
		sc.Auth = data
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// Writes raw browser_history source config so tests can cover malformed auth payloads and edge states.
func saveRawSourceConfig(t *testing.T, dataDir string, enabled bool, auth []byte) {
	t.Helper()
	if err := core.UpdateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Enabled = enabled
		sc.Reset = false
		sc.Auth = auth
	}); err != nil {
		t.Fatalf("save raw config: %v", err)
	}
}

// Builds a minimal MCP server with browser_history tools registered for JSON-RPC tests.
func buildMCPServer(t *testing.T, src *Source) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	return s
}

// Invokes one MCP tool through JSON-RPC and returns text payload plus error flag.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) (string, bool) {
	t.Helper()
	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	resp := s.HandleMessage(context.Background(), msg)
	data, _ := json.Marshal(resp)

	var parsed struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal response: %v raw=%s", err, string(data))
	}
	if len(parsed.Result.Content) == 0 {
		return "", parsed.Result.IsError
	}
	return parsed.Result.Content[0].Text, parsed.Result.IsError
}

// Captures stdout and stderr for one function call so CLI tests can assert on user-facing messages.
func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	fn()

	if err := stdoutW.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(stdout), string(stderr)
}
