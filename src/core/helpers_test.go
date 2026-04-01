package core

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestIntArg_int(t *testing.T) {
	args := map[string]interface{}{"n": 42}
	if got := IntArg(args, "n", 0); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestIntArg_float64(t *testing.T) {
	args := map[string]interface{}{"n": float64(7)}
	if got := IntArg(args, "n", 0); got != 7 {
		t.Errorf("expected 7, got %d", got)
	}
}

func TestIntArg_missing(t *testing.T) {
	args := map[string]interface{}{}
	if got := IntArg(args, "n", 99); got != 99 {
		t.Errorf("expected default 99, got %d", got)
	}
}

func TestJsonResult_valid(t *testing.T) {
	result, err := JsonResult(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("JsonResult: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

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

func TestToolDescription_withExample(t *testing.T) {
	got := ToolDescription("Search contacts.", `{"query":"alice"}`)
	want := `Search contacts. Example arguments: {"query":"alice"}`
	if got != want {
		t.Fatalf("ToolDescription() = %q, want %q", got, want)
	}
}

func TestToolDescription_withoutExample(t *testing.T) {
	got := ToolDescription("Search contacts.", "")
	if got != "Search contacts." {
		t.Fatalf("ToolDescription() = %q", got)
	}
}

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
	if !strings.Contains(text, `{\"query\":\"family dinner\"}`) {
		t.Fatalf("expected example arguments in error, got %s", text)
	}
}

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
	if !strings.Contains(text, `{\"limit\":7}`) {
		t.Fatalf("expected example arguments in error, got %s", text)
	}
}

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
	if !strings.Contains(text, `{\"include_raw\":true}`) {
		t.Fatalf("expected example arguments in error, got %s", text)
	}
}

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

func TestNormalizeConfig_seedsKnownSources(t *testing.T) {
	RegisterKnownSource("core_test_seeded")
	cfg := NormalizeConfig(Config{})
	if _, ok := cfg.Sources["core_test_seeded"]; !ok {
		t.Fatal("expected known source to be seeded")
	}
}

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
