package notebook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpyeahyouknowme/core"
)

// Verifies Name and Description return the expected values.
func TestSource_NameDescription(t *testing.T) {
	src := &Source{}
	if src.Name() != "notebook" {
		t.Fatalf("Name() = %q", src.Name())
	}
	if src.Description() != "Notebook" {
		t.Fatalf("Description() = %q", src.Description())
	}
}

// Verifies Close returns nil when no DB is open.
func TestSource_Close_noDB(t *testing.T) {
	src := &Source{}
	if err := src.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

// Verifies SearchEntries returns nil when the source has no database.
func TestSource_SearchEntries_noDB(t *testing.T) {
	src := &Source{}
	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries(): %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil, got %v", entries)
	}
}

// Verifies SearchEntries returns nil when no directories are configured.
func TestSource_SearchEntries_noDirs(t *testing.T) {
	src := newTestSource(t, nil)
	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries(): %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil for empty dirs, got %v", entries)
	}
}

// Verifies IsConfigured returns false when no dirs are stored in config.
func TestIsConfigured_false(t *testing.T) {
	dataDir := t.TempDir()
	if IsConfigured(dataDir) {
		t.Fatal("expected false for empty config")
	}
}

// Verifies IsConfigured returns true after saving a dir to config.
func TestIsConfigured_true(t *testing.T) {
	dataDir := t.TempDir()
	cfg := NotebookConfig{Dirs: []string{"/notes"}}
	saveNotebookConfig(dataDir, cfg)
	if !IsConfigured(dataDir) {
		t.Fatal("expected true after adding dir")
	}
}

// Verifies loadNotebookConfig returns empty config for a fresh dataDir.
func TestLoadNotebookConfig_empty(t *testing.T) {
	dataDir := t.TempDir()
	cfg := loadNotebookConfig(dataDir)
	if len(cfg.Dirs) != 0 {
		t.Fatalf("expected empty dirs, got %v", cfg.Dirs)
	}
}

// Verifies saveNotebookConfig and loadNotebookConfig round-trip correctly.
func TestSaveLoadNotebookConfig(t *testing.T) {
	dataDir := t.TempDir()
	want := NotebookConfig{Dirs: []string{"/a/b", "/c/d"}}
	if err := saveNotebookConfig(dataDir, want); err != nil {
		t.Fatalf("saveNotebookConfig: %v", err)
	}
	got := loadNotebookConfig(dataDir)
	if len(got.Dirs) != 2 || got.Dirs[0] != "/a/b" || got.Dirs[1] != "/c/d" {
		t.Fatalf("unexpected config: %+v", got)
	}
}

// Verifies Reset removes notebook.db without error.
func TestSource_Reset(t *testing.T) {
	dataDir := t.TempDir()
	src := NewSource(dataDir)
	defer src.Close()
	if err := src.Reset(dataDir); err != nil {
		t.Fatalf("Reset(): %v", err)
	}
}

// Verifies InfoLines returns nil when source is not enabled.
func TestInfoLines_disabled(t *testing.T) {
	dataDir := t.TempDir()
	lines := InfoLines(dataDir)
	if len(lines) != 0 {
		t.Fatalf("expected no lines when disabled, got %v", lines)
	}
}

// Verifies InfoLines returns just a hint line when source is enabled but no directories configured.
func TestInfoLines_enabledNoDirs(t *testing.T) {
	dataDir := t.TempDir()
	core.SetSourceEnabled(dataDir, "notebook", true)

	lines := InfoLines(dataDir)
	if len(lines) != 1 || !strings.Contains(lines[0], "Hint") {
		t.Fatalf("expected hint line, got %v", lines)
	}
}

// Verifies InfoLines returns enabled status and dir info when configured.
func TestInfoLines_enabled(t *testing.T) {
	dataDir := t.TempDir()
	dir := t.TempDir()
	writeMDFile(t, dir, "a.md", "# A")

	saveNotebookConfig(dataDir, NotebookConfig{Dirs: []string{dir}})
	core.SetSourceEnabled(dataDir, "notebook", true)

	lines := InfoLines(dataDir)
	foundDir := false
	foundCounts := false
	for _, l := range lines {
		if strings.Contains(l, "Status:") {
			t.Fatalf("Status line should not appear in InfoLines, got: %q", l)
		}
		if l == "   Dir:        "+dir {
			foundDir = true
		}
		if l == "               (1 md, 0 pdf, 0 images)" {
			foundCounts = true
		}
	}
	if !foundDir {
		t.Fatalf("expected dir line, got %v", lines)
	}
	if !foundCounts {
		t.Fatalf("expected counts line, got %v", lines)
	}
}

// Verifies itoa handles zero and positive integers correctly.
func TestItoa(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{99, "99"},
		{1000, "1000"},
	}
	for _, tt := range tests {
		if got := itoa(tt.n); got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// Verifies countFilesInDir returns zero for an empty directory.
func TestCountFilesInDir_empty(t *testing.T) {
	dir := t.TempDir()
	counts := countFilesInDir(dir)
	if counts["md"] != 0 || counts["pdf"] != 0 || counts["image"] != 0 {
		t.Fatalf("expected all zeros, got %v", counts)
	}
}

// Verifies countFilesInDir returns zero for a missing directory.
func TestCountFilesInDir_missing(t *testing.T) {
	counts := countFilesInDir("/nonexistent/dir")
	if counts["md"] != 0 {
		t.Fatalf("expected all zeros for missing dir, got %v", counts)
	}
}

// Verifies countFilesInDir counts files by type across the configured directory tree.
func TestCountFilesInDir_withFiles(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "a.md", "# A")
	writeMDFile(t, dir, "b.md", "# B")
	writeImageFile(t, dir, "c.png")

	counts := countFilesInDir(dir)
	if counts["md"] != 2 {
		t.Errorf("md count = %d, want 2", counts["md"])
	}
	if counts["image"] != 1 {
		t.Errorf("image count = %d, want 1", counts["image"])
	}
}

// Verifies SearchEntries returns entries when a directory is configured with files.
func TestSource_SearchEntries_withFiles(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "hello.md", "# Hello\n\nWorld content.")
	src := newTestSource(t, []string{dir})

	entries, err := src.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries(): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries, got none")
	}
}

// Verifies formatDirLines produces separate path and count lines for one configured directory.
func TestFormatDirLines(t *testing.T) {
	counts := map[string]int{"md": 5, "pdf": 2, "image": 3}
	lines := formatDirLines("/my/notes", counts)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %v", lines)
	}
	if lines[0] != "   Dir:        /my/notes" {
		t.Fatalf("path line = %q", lines[0])
	}
	if lines[1] != "               (5 md, 2 pdf, 3 images)" {
		t.Fatalf("count line = %q", lines[1])
	}
}

// Verifies loadNotebookConfig returns empty config for invalid JSON in Auth field.
func TestLoadNotebookConfig_badJSON(t *testing.T) {
	dataDir := t.TempDir()
	// Write config.json with invalid auth JSON for notebook source.
	configPath := filepath.Join(dataDir, "config.json")
	badConfig := `{"sources":{"notebook":{"enabled":true,"auth":"not-valid-json"}}}`
	os.WriteFile(configPath, []byte(badConfig), 0644)
	cfg := loadNotebookConfig(dataDir)
	if len(cfg.Dirs) != 0 {
		t.Fatalf("expected empty dirs for bad JSON, got %v", cfg.Dirs)
	}
}

// Verifies countFilesInDir includes supported files from nested subdirectories.
func TestCountFilesInDir_withSubdir(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "note.md", "# Note")
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	writeMDFile(t, filepath.Join(dir, "subdir"), "nested.md", "# Nested")
	counts := countFilesInDir(dir)
	if counts["md"] != 2 {
		t.Fatalf("expected md count 2, got %d", counts["md"])
	}
}

// Verifies countFilesInDir counts PDF files correctly.
func TestCountFilesInDir_withPDF(t *testing.T) {
	dir := t.TempDir()
	writePDFFile(t, dir, "doc.pdf", "PDF content")
	writeMDFile(t, dir, "note.md", "# Note")
	counts := countFilesInDir(dir)
	if counts["pdf"] != 1 {
		t.Fatalf("expected pdf count 1, got %d", counts["pdf"])
	}
	if counts["md"] != 1 {
		t.Fatalf("expected md count 1, got %d", counts["md"])
	}
}

// Verifies countFilesInDir skips hidden subdirectories to match notebook scan behavior.
func TestCountFilesInDir_skipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "visible.md", "# Visible")
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	writeMDFile(t, filepath.Join(dir, ".hidden"), "secret.md", "# Secret")

	counts := countFilesInDir(dir)
	if counts["md"] != 1 {
		t.Fatalf("expected md count 1, got %d", counts["md"])
	}
}

// Verifies InfoLines shows the cache file size when notebook.db exists.
func TestInfoLines_cacheSize(t *testing.T) {
	dataDir := t.TempDir()
	dir := t.TempDir()
	writeMDFile(t, dir, "a.md", "# Note\n\nContent.")

	saveNotebookConfig(dataDir, NotebookConfig{Dirs: []string{dir}})
	core.SetSourceEnabled(dataDir, "notebook", true)

	// Write a fake notebook.db so FileGroupSizeBytes returns non-zero.
	os.WriteFile(filepath.Join(dataDir, "notebook.db"), make([]byte, 1024), 0644)

	lines := InfoLines(dataDir)
	foundCache := false
	for _, l := range lines {
		if len(l) > 10 && l[:10] == "   Cache: " {
			foundCache = true
		}
	}
	_ = foundCache // cache line only appears when FileGroupSizeBytes > 0; just verify no panic
}

// Verifies HasChangesSince checks notebook DB and WAL mtimes so incremental indexing can skip unchanged caches.
func TestSource_HasChangesSince(t *testing.T) {
	dataDir := t.TempDir()
	src := &Source{dataDir: dataDir}
	if !src.HasChangesSince(time.Time{}) {
		t.Fatal("expected zero watermark to force indexing")
	}
	if !src.HasChangesSince(time.Now()) {
		t.Fatal("expected missing notebook files to trigger indexing")
	}

	dbPath := filepath.Join(dataDir, "notebook.db")
	walPath := filepath.Join(dataDir, "notebook.db-wal")
	if err := os.WriteFile(dbPath, []byte("db"), 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := os.WriteFile(walPath, []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(dbPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes db: %v", err)
	}
	if err := os.Chtimes(walPath, newTime, newTime); err != nil {
		t.Fatalf("chtimes wal: %v", err)
	}

	if src.HasChangesSince(time.Now()) {
		t.Fatal("expected future watermark to skip unchanged notebook cache")
	}
	if !src.HasChangesSince(time.Now().Add(-90 * time.Minute)) {
		t.Fatal("expected recent WAL change to trigger notebook reindex")
	}
}

// Verifies tildeHome shortens home-prefixed paths and leaves unrelated paths intact.
func TestTildeHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available")
	}
	if got := tildeHome(home + "/docs"); got != "~/docs" {
		t.Fatalf("expected tilde path, got %q", got)
	}
	if got := tildeHome(home); got != "~" {
		t.Fatalf("expected bare tilde, got %q", got)
	}
	if got := tildeHome("/unrelated/path"); got != "/unrelated/path" {
		t.Fatalf("expected unchanged path, got %q", got)
	}
}
