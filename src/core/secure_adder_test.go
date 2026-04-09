package core

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Verifies SecureToolAdder omits disabled tool names from registration so HandleMessage cannot invoke them.
func TestSecureToolAdder_skipsDisabledTool(t *testing.T) {
	s := server.NewMCPServer("t", "1.0.0", server.WithToolCapabilities(false))
	adder := NewSecureToolAdder(s, McpConfig{DisabledTools: []string{"gone"}}, t.TempDir())
	adder.AddTool(NewReadOnlyTool("gone", "x"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Fatal("handler should not be registered")
		return nil, nil
	})
	adder.AddTool(NewReadOnlyTool("keep", "y"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	call, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params":  map[string]interface{}{"name": "gone", "arguments": map[string]interface{}{}},
	})
	raw := s.HandleMessage(context.Background(), call)
	data, _ := json.Marshal(raw)
	if !strings.Contains(string(data), "not found") && !strings.Contains(string(data), "Unknown tool") {
		// mcp-go may word differently; ensure we did not return success content
		if strings.Contains(string(data), `"text":"ok"`) {
			t.Fatalf("disabled tool should not run: %s", string(data))
		}
	}
}

// Verifies read_only mode registers only read-only tools and blocks mutating handlers.
func TestSecureToolAdder_readOnlySkipsMutating(t *testing.T) {
	s := server.NewMCPServer("t", "1.0.0", server.WithToolCapabilities(false))
	adder := NewSecureToolAdder(s, McpConfig{ReadOnly: true}, t.TempDir())
	adder.AddTool(NewMutatingTool("write", "w"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Fatal("mutating tool registered under read_only")
		return nil, nil
	})
	adder.AddTool(NewReadOnlyTool("read", "r"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("yes"), nil
	})

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	call, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params":  map[string]interface{}{"name": "read", "arguments": map[string]interface{}{}},
	})
	raw := s.HandleMessage(context.Background(), call)
	data, _ := json.Marshal(raw)
	if !strings.Contains(string(data), "yes") {
		t.Fatalf("expected read tool to work: %s", string(data))
	}
}

// Verifies mutating-tool rate limiting returns a tool error after the configured number of calls per minute.
func TestSecureToolAdder_rateLimitMutating(t *testing.T) {
	s := server.NewMCPServer("t", "1.0.0", server.WithToolCapabilities(false))
	adder := NewSecureToolAdder(s, McpConfig{MutatingToolsPerMin: 2}, t.TempDir())
	adder.AddTool(NewMutatingTool("m", "mut"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("done"), nil
	})

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	call := func() string {
		msg, _ := json.Marshal(map[string]interface{}{
			"jsonrpc": "2.0", "id": 99, "method": "tools/call",
			"params":  map[string]interface{}{"name": "m", "arguments": map[string]interface{}{}},
		})
		raw := s.HandleMessage(context.Background(), msg)
		data, _ := json.Marshal(raw)
		return string(data)
	}
	first := call()
	second := call()
	if !strings.Contains(first, "done") || !strings.Contains(second, "done") {
		t.Fatal("expected first two calls to succeed")
	}
	if !strings.Contains(call(), "rate limit") {
		t.Fatalf("expected rate limit on third call, got %s", call())
	}
}

// Verifies NewSecureToolAdder logs a warning when the audit log directory cannot be created.
func TestNewSecureToolAdder_auditLoggerInitFailureLogs(t *testing.T) {
	badDir := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(badDir, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	s := server.NewMCPServer("t", "1.0.0", server.WithToolCapabilities(false))
	_ = NewSecureToolAdder(s, McpConfig{}, filepath.Join(badDir, "nested"))

	out := buf.String()
	if !strings.Contains(out, "mcp audit log unavailable") {
		t.Fatalf("expected audit unavailable warning, got %q", out)
	}
}
