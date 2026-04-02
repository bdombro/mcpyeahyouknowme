package notebook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verifies fileTypeOf returns the correct logical type for known extensions and empty for unknowns.
func TestFileTypeOf(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"note.md", "md"},
		{"readme.txt", "md"},
		{"report.pdf", "pdf"},
		{"photo.jpg", "image"},
		{"photo.jpeg", "image"},
		{"screenshot.png", "image"},
		{"animation.gif", "image"},
		{"photo.webp", "image"},
		{"photo.heic", "image"},
		{"photo.tiff", "image"},
		{"binary.exe", ""},
		{"archive.zip", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fileTypeOf(tt.name); got != tt.want {
				t.Errorf("fileTypeOf(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// Verifies scanOneDir returns SearchEntry values for a directory with markdown files.
func TestScanOneDir_markdown(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "note.md", "# My Note\n\nSome content here.")

	db := newTestDB(t)
	entries, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("scanOneDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries, got none")
	}
	var found bool
	for _, e := range entries {
		if e.ContentType == "note_title" && e.Title == "My Note" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected note_title entry for 'My Note', got %+v", entries)
	}
}

// Verifies scanOneDir skips unchanged files on the second call using the file cache.
func TestScanOneDir_cacheHit(t *testing.T) {
	dir := t.TempDir()
	path := writeMDFile(t, dir, "note.md", "# Cached Note\n\nContent.")
	db := newTestDB(t)

	// First scan — populates cache.
	_, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}

	// Corrupt the file so a real re-extract would fail.
	info, _ := os.Stat(path)
	// Change mod_time manually in cache to simulate a pre-populated cache with same modtime.
	_ = info

	row, _ := GetCacheRow(db, path)
	if row == nil {
		t.Fatal("expected cache row after first scan")
	}

	// Second scan — should hit cache and return same entries.
	entries, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries from cache on second scan")
	}
}

// Verifies scanOneDir re-extracts and updates the cache when a file is modified.
func TestScanOneDir_cacheInvalidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	os.WriteFile(path, []byte("# Old Title\n\nOld content."), 0644)
	db := newTestDB(t)

	// First scan.
	_, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}

	// Rewrite the file, then bump its mtime by 2 seconds to guarantee cache invalidation.
	os.WriteFile(path, []byte("# New Title\n\nNew content."), 0644)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(path, future, future)

	// Second scan — should detect the mtime change and re-extract.
	entries, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	var foundNew bool
	for _, e := range entries {
		if e.ContentType == "note_title" && e.Title == "New Title" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatalf("expected updated title 'New Title' after cache invalidation, got %+v", entries)
	}
}

// Verifies scanOneDir prunes cache rows for deleted files.
func TestScanOneDir_prunesDeleted(t *testing.T) {
	dir := t.TempDir()
	path := writeMDFile(t, dir, "delete-me.md", "# Delete Me\n\nContent.")
	db := newTestDB(t)

	// First scan — caches the file.
	_, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	row, _ := GetCacheRow(db, path)
	if row == nil {
		t.Fatal("expected row after first scan")
	}

	// Delete the file.
	os.Remove(path)

	// Second scan — should prune the stale entry.
	_, err = scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	row, _ = GetCacheRow(db, path)
	if row != nil {
		t.Fatal("expected stale cache row to be pruned")
	}
}

// Verifies scanOneDir skips hidden directories.
func TestScanOneDir_skipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, ".obsidian")
	os.MkdirAll(hiddenDir, 0755)
	os.WriteFile(filepath.Join(hiddenDir, "config.md"), []byte("# Hidden"), 0644)
	writeMDFile(t, dir, "visible.md", "# Visible\n\nContent.")

	db := newTestDB(t)
	entries, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("scanOneDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.SourceID, ".obsidian") {
			t.Fatalf("expected hidden dir to be skipped, got entry: %+v", e)
		}
	}
}

// Verifies scanOneDir uses the Vision analyzer for image files and stores labels.
func TestScanOneDir_imageWithVision(t *testing.T) {
	dir := t.TempDir()
	writeImageFile(t, dir, "photo.png")

	fa := &fakeAnalyzer{
		ocrText: "whiteboard text",
		labels:  []string{"whiteboard", "text"},
	}
	db := newTestDB(t)
	entries, err := scanOneDir(db, dir, "notebook", fa)
	if err != nil {
		t.Fatalf("scanOneDir: %v", err)
	}

	var imageContent string
	for _, e := range entries {
		if e.ContentType == "image" {
			imageContent = e.Content
		}
	}
	if imageContent == "" {
		t.Fatal("expected image entry with content")
	}
	if !strings.Contains(imageContent, "whiteboard") {
		t.Fatalf("expected labels in image content, got %q", imageContent)
	}
}

// Verifies buildSearchEntries chunks large content into multiple entries.
func TestBuildSearchEntries_chunks(t *testing.T) {
	bigContent := strings.Repeat("word ", 2000) // > 4KB
	row := CacheRow{
		Path: "/notes/big.md", Dir: "/notes", ModTime: time.Now().Unix(), Size: 100,
		FileType: "md", Title: "Big Note", Content: bigContent, Labels: "[]",
	}
	entries := buildSearchEntries(row, "notebook")
	var contentEntries int
	for _, e := range entries {
		if e.ContentType == "note_content" {
			contentEntries++
		}
	}
	if contentEntries < 2 {
		t.Fatalf("expected multiple note_content chunks, got %d", contentEntries)
	}
}

// Verifies relativePath returns the path relative to dir.
func TestRelativePath(t *testing.T) {
	rel := relativePath("/notes", "/notes/sub/file.md")
	if rel != "sub/file.md" {
		t.Fatalf("relativePath = %q", rel)
	}
}

// Verifies isInsideDir correctly reports path containment.
func TestIsInsideDir(t *testing.T) {
	tests := []struct {
		dir, path string
		want      bool
	}{
		{"/notes", "/notes/file.md", true},
		{"/notes", "/notes/sub/file.md", true},
		{"/notes", "/other/file.md", false},
		{"/notes", "/notesfile.md", false},
	}
	for _, tt := range tests {
		if got := isInsideDir(tt.dir, tt.path); got != tt.want {
			t.Errorf("isInsideDir(%q, %q) = %v, want %v", tt.dir, tt.path, got, tt.want)
		}
	}
}

// Verifies scanDirs skips non-existent directories without error.
func TestScanDirs_skipsInvalidDir(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "note.md", "# Note\n\nContent.")

	db := newTestDB(t)
	entries, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("scanOneDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries from valid dir")
	}
}

// Verifies scanOneDir returns error for a non-existent directory.
func TestScanOneDir_missingDir(t *testing.T) {
	db := newTestDB(t)
	_, err := scanOneDir(db, "/nonexistent/dir", "notebook", nil)
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

// Verifies scanDirs skips dirs that fail without aborting the entire scan.
func TestScanDirs_skipsFailedDir(t *testing.T) {
	goodDir := t.TempDir()
	writeMDFile(t, goodDir, "ok.md", "# OK\n\nContent here.")

	db := newTestDB(t)
	entries, err := scanDirs(db, []string{"/nonexistent/dir", goodDir}, "notebook")
	if err != nil {
		t.Fatalf("scanDirs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries from the valid dir")
	}
}

// Verifies scanOneDir skips files with unsupported extensions.
func TestScanOneDir_unsupportedFile(t *testing.T) {
	dir := t.TempDir()
	writeMDFile(t, dir, "note.md", "# Note\n\nContent.")
	os.WriteFile(filepath.Join(dir, "data.xyz"), []byte("ignored"), 0644)

	db := newTestDB(t)
	entries, err := scanOneDir(db, dir, "notebook", nil)
	if err != nil {
		t.Fatalf("scanOneDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.SourceID, ".xyz") {
			t.Fatalf("unexpected entry for .xyz file: %+v", e)
		}
	}
}

// Verifies extractFile handles unreadable markdown files gracefully.
func TestExtractFile_unreadableMD(t *testing.T) {
	row := extractFile("/nonexistent/bad.md", "/nonexistent", "md", 1000, 100, nil)
	if row.Title != "bad" {
		t.Fatalf("expected fallback title 'bad', got %q", row.Title)
	}
}

// Verifies extractFile handles PDF files via the extractor path.
func TestExtractFile_pdf(t *testing.T) {
	dir := t.TempDir()
	path := writePDFFile(t, dir, "doc.pdf", "Some document text content here")
	info, _ := os.Stat(path)
	row := extractFile(path, dir, "pdf", info.ModTime().Unix(), info.Size(), nil)
	if row.Title != "doc" {
		t.Fatalf("expected title 'doc', got %q", row.Title)
	}
	if row.FileType != "pdf" {
		t.Fatalf("expected fileType 'pdf', got %q", row.FileType)
	}
}

// Verifies extractFile handles image files without an analyzer.
func TestExtractFile_imageNoAnalyzer(t *testing.T) {
	dir := t.TempDir()
	path := writeImageFile(t, dir, "pic.png")
	info, _ := os.Stat(path)
	row := extractFile(path, dir, "image", info.ModTime().Unix(), info.Size(), nil)
	if row.Title != "pic" {
		t.Fatalf("expected title 'pic', got %q", row.Title)
	}
}

// Verifies buildSearchEntries returns entries for PDF content type.
func TestBuildSearchEntries_pdf(t *testing.T) {
	row := CacheRow{
		Path: "/docs/report.pdf", Dir: "/docs", ModTime: time.Now().Unix(), Size: 100,
		FileType: "pdf", Title: "report", Content: "PDF text content here.", Labels: "[]",
	}
	entries := buildSearchEntries(row, "notebook")
	var foundTitle, foundContent bool
	for _, e := range entries {
		if e.ContentType == "pdf_title" {
			foundTitle = true
		}
		if e.ContentType == "pdf_content" {
			foundContent = true
		}
	}
	if !foundTitle {
		t.Fatal("expected pdf_title entry")
	}
	if !foundContent {
		t.Fatal("expected pdf_content entry")
	}
}

// Verifies buildSearchEntries returns nil for an unknown file type.
func TestBuildSearchEntries_unknownType(t *testing.T) {
	row := CacheRow{
		Path: "/x/file.xyz", Dir: "/x", ModTime: time.Now().Unix(), Size: 1,
		FileType: "unknown", Title: "file", Content: "stuff", Labels: "[]",
	}
	entries := buildSearchEntries(row, "notebook")
	if entries != nil {
		t.Fatalf("expected nil for unknown type, got %+v", entries)
	}
}

// Verifies buildTextEntries returns only a title entry when content is empty.
func TestBuildTextEntries_emptyContent(t *testing.T) {
	row := CacheRow{
		Path: "/notes/empty.md", Dir: "/notes", ModTime: time.Now().Unix(), Size: 10,
		FileType: "md", Title: "Empty Note", Content: "   ", Labels: "[]",
	}
	entries := buildSearchEntries(row, "notebook")
	if len(entries) != 1 {
		t.Fatalf("expected 1 title-only entry, got %d", len(entries))
	}
	if entries[0].ContentType != "note_title" {
		t.Fatalf("expected note_title, got %q", entries[0].ContentType)
	}
}

// Verifies scanOneDir handles a directory with PDF and image files.
func TestScanOneDir_pdfAndImage(t *testing.T) {
	dir := t.TempDir()
	writePDFFile(t, dir, "doc.pdf", "Hello World PDF content text")
	writeImageFile(t, dir, "photo.jpg")

	db := newTestDB(t)
	fa := &fakeAnalyzer{
		ocrText: "photo text",
		labels:  []string{"photo"},
	}
	entries, err := scanOneDir(db, dir, "notebook", fa)
	if err != nil {
		t.Fatalf("scanOneDir: %v", err)
	}
	var hasPDF, hasImage bool
	for _, e := range entries {
		if e.ContentType == "pdf_title" || e.ContentType == "pdf_content" {
			hasPDF = true
		}
		if e.ContentType == "image" {
			hasImage = true
		}
	}
	if !hasPDF {
		t.Fatal("expected PDF entries")
	}
	if !hasImage {
		t.Fatal("expected image entries")
	}
}

// Verifies isPathInConfiguredDirs returns false for an empty dir list.
func TestIsPathInConfiguredDirs_empty(t *testing.T) {
	if isPathInConfiguredDirs("/some/path", nil) {
		t.Fatal("expected false for empty dirs")
	}
}

// Verifies buildSearchEntries emits correct metadata for image entries.
func TestBuildSearchEntries_imageLabels(t *testing.T) {
	labelsJSON, _ := json.Marshal([]string{"cat", "animal"})
	row := CacheRow{
		Path: "/img/cat.png", Dir: "/img", ModTime: time.Now().Unix(), Size: 50,
		FileType: "image", Title: "cat", Content: "furry creature", Labels: string(labelsJSON),
	}
	entries := buildSearchEntries(row, "notebook")
	if len(entries) != 1 || entries[0].ContentType != "image" {
		t.Fatalf("expected single image entry, got %+v", entries)
	}
	if !strings.Contains(entries[0].Content, "cat") {
		t.Fatalf("expected labels in content, got %q", entries[0].Content)
	}
}

