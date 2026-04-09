package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verifies summarizeArgsForAudit redacts sensitive keys by length instead of echoing raw values.
func TestSummarizeArgsForAudit_redactsSensitiveKeys(t *testing.T) {
	args := map[string]interface{}{
		"query":   strings.Repeat("x", 100),
		"limit":   float64(5),
		"message": "secret-body-text",
	}
	s := summarizeArgsForAudit(args, 500)
	if strings.Contains(s, "secret-body-text") {
		t.Fatalf("expected message redacted, got %s", s)
	}
	if !strings.Contains(s, "redacted len=") {
		t.Fatalf("expected redacted marker, got %s", s)
	}
}

// Verifies NewAuditLogger appends JSON lines and the file is readable.
func TestAuditLogger_Log_line(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLogger(dir)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	log.Log("search", map[string]interface{}{"limit": float64(1)})
	path := filepath.Join(dir, "mcp-audit.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var line struct {
		Tool        string `json:"tool"`
		ArgsSummary string `json:"args_summary"`
	}
	if err := json.Unmarshal(data[:len(data)-1], &line); err != nil { // trim newline
		t.Fatalf("unmarshal line: %v raw=%s", err, string(data))
	}
	if line.Tool != "search" {
		t.Fatalf("tool=%q", line.Tool)
	}
}
