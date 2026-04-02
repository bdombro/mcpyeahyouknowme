package notebook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verifies notebook_list returns all files when no filters are applied.
func TestMCP_List_noFilter(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "note.md", "# Note\n\nContent.")
	writeImageFile(t, dir, "photo.png")

	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_list", map[string]interface{}{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var results []FileInfo
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("unmarshal: %v — raw: %s", err, text)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 files, got %d: %s", len(results), text)
	}
}

// Verifies notebook_list filters by type when the type parameter is provided.
func TestMCP_List_typeFilter(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "note.md", "# Note\n\nContent.")
	writeImageFile(t, dir, "photo.png")

	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_list", map[string]interface{}{"type": "md"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var results []FileInfo
	json.Unmarshal([]byte(text), &results)
	for _, r := range results {
		if r.Type != "md" {
			t.Fatalf("expected only md files, got type %q", r.Type)
		}
	}
}

// Verifies notebook_list filters by query substring.
func TestMCP_List_queryFilter(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "project-alpha.md", "# Alpha")
	writeMDFile(t, dir, "project-beta.md", "# Beta")

	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_list", map[string]interface{}{"query": "alpha"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var results []FileInfo
	json.Unmarshal([]byte(text), &results)
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'alpha', got %d: %s", len(results), text)
	}
	if !strings.Contains(results[0].Path, "alpha") {
		t.Fatalf("unexpected result: %+v", results[0])
	}
}

// Verifies notebook_read returns file contents for a valid path.
func TestMCP_Read_success(t *testing.T) {
	dir := t.TempDir()
	content := "# Hello\n\nThis is my note."
	writeMDFile(t, dir, "hello.md", content)

	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_read", map[string]interface{}{
		"path": filepath.Join(dir, "hello.md"),
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "This is my note.") {
		t.Fatalf("expected file content, got %q", text)
	}
}

// Verifies notebook_read returns an error when path is missing.
func TestMCP_Read_missingArg(t *testing.T) {
	src := newTestSource(t, nil)
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_read", map[string]interface{}{})
	if !isErr {
		t.Fatalf("expected error for missing path, got %q", text)
	}
	if !strings.Contains(text, "path parameter is required") {
		t.Fatalf("unexpected error: %q", text)
	}
}

// Verifies notebook_read returns an error when path is outside configured dirs.
func TestMCP_Read_outsideDir(t *testing.T) {
	dir := t.TempDir()
	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_read", map[string]interface{}{
		"path": "/etc/passwd",
	})
	if !isErr {
		t.Fatalf("expected error for path outside dirs, got %q", text)
	}
	if !strings.Contains(text, "not inside any configured") {
		t.Fatalf("unexpected error: %q", text)
	}
}

// Verifies notebook_read_pdf returns an error when path is missing.
func TestMCP_ReadPDF_missingArg(t *testing.T) {
	src := newTestSource(t, nil)
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_read_pdf", map[string]interface{}{})
	if !isErr {
		t.Fatalf("expected error for missing path, got %q", text)
	}
	if !strings.Contains(text, "path parameter is required") {
		t.Fatalf("unexpected error: %q", text)
	}
}

// Verifies notebook_read_pdf returns an error when path is outside configured dirs.
func TestMCP_ReadPDF_outsideDir(t *testing.T) {
	dir := t.TempDir()
	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_read_pdf", map[string]interface{}{
		"path": "/usr/share/doc/some.pdf",
	})
	if !isErr {
		t.Fatalf("expected error for path outside dirs, got %q", text)
	}
}

// handleReadPDF calls extractPDF(path, VisionAnalyzer{}) which invokes real CGO Vision
// for scanned PDFs (< 10 words), so MCP-level PDF tests are covered by extractPDF unit tests.

// Verifies notebook_get_image returns an error when path is missing.
func TestMCP_GetImage_missingArg(t *testing.T) {
	src := newTestSource(t, nil)
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_get_image", map[string]interface{}{})
	if !isErr {
		t.Fatalf("expected error for missing path, got %q", text)
	}
}

// Verifies notebook_get_image returns an error when path is outside configured dirs.
func TestMCP_GetImage_outsideDir(t *testing.T) {
	dir := t.TempDir()
	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_get_image", map[string]interface{}{
		"path": "/tmp/hacker.jpg",
	})
	if !isErr {
		t.Fatalf("expected error for outside-dir path, got %q", text)
	}
}

// Verifies notebook_get_image returns base64 content for a valid image path.
func TestMCP_GetImage_success(t *testing.T) {
	dir := t.TempDir()
	imgPath := writeImageFile(t, dir, "photo.png")

	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_get_image", map[string]interface{}{"path": imgPath})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshal: %v — raw: %s", err, text)
	}
	if result["base64"] == "" {
		t.Fatalf("expected base64 content, got empty")
	}
}

// Verifies resolvePath resolves a relative path against the first matching configured directory.
func TestResolvePath_relative(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "note.md"), []byte("# Note"), 0644)
	cfg := NotebookConfig{Dirs: []string{dir}}

	resolved := resolvePath(cfg, "note.md")
	expected := filepath.Join(dir, "note.md")
	if resolved != expected {
		t.Fatalf("resolvePath(%q) = %q, want %q", "note.md", resolved, expected)
	}
}

// Verifies resolvePath returns an absolute path unchanged.
func TestResolvePath_absolute(t *testing.T) {
	cfg := NotebookConfig{Dirs: []string{"/notes"}}
	abs := "/notes/doc.md"
	if got := resolvePath(cfg, abs); got != abs {
		t.Fatalf("resolvePath(%q) = %q, want %q", abs, got, abs)
	}
}

// Verifies resolvePath returns the raw path when it doesn't match any configured directory.
func TestResolvePath_noMatch(t *testing.T) {
	cfg := NotebookConfig{Dirs: []string{"/notes"}}
	raw := "nonexistent/path.md"
	if got := resolvePath(cfg, raw); got != raw {
		t.Fatalf("resolvePath(%q) = %q, want %q", raw, got, raw)
	}
}

// Verifies notebook_read returns an error for a missing file inside a configured directory.
func TestMCP_Read_missingFile(t *testing.T) {
	dir := t.TempDir()
	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_read", map[string]interface{}{
		"path": filepath.Join(dir, "doesnt-exist.md"),
	})
	if !isErr {
		t.Fatalf("expected error for missing file, got %q", text)
	}
}

// Verifies notebook_get_image returns an error for a missing file inside a configured directory.
func TestMCP_GetImage_missingFile(t *testing.T) {
	dir := t.TempDir()
	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_get_image", map[string]interface{}{
		"path": filepath.Join(dir, "missing.png"),
	})
	if !isErr {
		t.Fatalf("expected error for missing file, got %q", text)
	}
}

// Verifies notebook_list skips dirs that no longer exist on disk.
func TestMCP_List_missingDir(t *testing.T) {
	dir := t.TempDir()
	src := newTestSource(t, []string{dir, "/nonexistent/dir"})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_list", map[string]interface{}{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
}

// Verifies notebook_list skips files with unsupported extensions.
func TestMCP_List_unsupportedFile(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "note.md", "# Note")
	os.WriteFile(filepath.Join(dir, "ignore.xyz"), []byte("data"), 0644)

	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_list", map[string]interface{}{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var results []FileInfo
	json.Unmarshal([]byte(text), &results)
	if len(results) != 1 {
		t.Fatalf("expected 1 result (only the .md), got %d", len(results))
	}
}

// Verifies notebook_list handles a directory with hidden subdirectories.
func TestMCP_List_hiddenDir(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "visible.md", "# Visible")
	hiddenDir := filepath.Join(dir, ".hidden")
	os.MkdirAll(hiddenDir, 0755)
	os.WriteFile(filepath.Join(hiddenDir, "secret.md"), []byte("# Secret"), 0644)

	src := newTestSource(t, []string{dir})
	s := buildMCPServer(t, src)

	text, isErr := callTool(t, s, "notebook_list", map[string]interface{}{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var results []FileInfo
	json.Unmarshal([]byte(text), &results)
	for _, r := range results {
		if strings.Contains(r.Path, ".hidden") {
			t.Fatalf("expected hidden dir to be skipped, got %+v", r)
		}
	}
}

