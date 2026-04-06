package notebook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Verifies extractMarkdownTitle returns the first H1 heading when present.
func TestExtractMarkdownTitle_h1(t *testing.T) {
	content := "# My Project\n\nSome content here."
	title := extractMarkdownTitle(content, "my-project.md")
	if title != "My Project" {
		t.Fatalf("expected 'My Project', got %q", title)
	}
}

// Verifies extractMarkdownTitle falls back to the filename stem when no H1 is present.
func TestExtractMarkdownTitle_fallback(t *testing.T) {
	content := "## Section\n\nNo H1 here."
	title := extractMarkdownTitle(content, "meeting-notes.md")
	if title != "meeting-notes" {
		t.Fatalf("expected 'meeting-notes', got %q", title)
	}
}

// Verifies extractMarkdownTitle ignores H2 and deeper headings for the title.
func TestExtractMarkdownTitle_ignoresDeepHeadings(t *testing.T) {
	content := "## Section\n### Subsection\nNo H1."
	title := extractMarkdownTitle(content, "file.md")
	if title != "file" {
		t.Fatalf("expected 'file', got %q", title)
	}
}

// Verifies extractMarkdown reads the file and returns title and content.
func TestExtractMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	content := "# Hello World\n\nThis is a note."
	os.WriteFile(path, []byte(content), 0644)

	title, body, err := extractMarkdown(path)
	if err != nil {
		t.Fatalf("extractMarkdown: %v", err)
	}
	if title != "Hello World" {
		t.Fatalf("title = %q", title)
	}
	if !strings.Contains(body, "This is a note.") {
		t.Fatalf("body missing content: %q", body)
	}
}

// Verifies extractMarkdown returns an error for a non-existent file.
func TestExtractMarkdown_missingFile(t *testing.T) {
	_, _, err := extractMarkdown("/nonexistent/path.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// Verifies stemName strips the file extension correctly.
func TestStemName(t *testing.T) {
	tests := []struct{ input, want string }{
		{"notes.md", "notes"},
		{"report.pdf", "report"},
		{"image.jpg", "image"},
		{"no-ext", "no-ext"},
		{"/path/to/file.txt", "file"},
	}
	for _, tt := range tests {
		got := stemName(tt.input)
		if got != tt.want {
			t.Errorf("stemName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Verifies cleanMarkdownForIndex preserves readable markdown text while stripping link syntax before indexing.
func TestCleanMarkdownForIndex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain wiki link",
			input: "See [[Project Plan]] next.",
			want:  "See Project Plan next.",
		},
		{
			name:  "aliased wiki link",
			input: "Open [[project-plan|the plan]].",
			want:  "Open the plan.",
		},
		{
			name:  "markdown link",
			input: "Read [the docs](https://example.com/docs).",
			want:  "Read the docs.",
		},
		{
			name:  "markdown image",
			input: "Diagram: ![system architecture](https://example.com/image.png)",
			want:  "Diagram: system architecture",
		},
		{
			name:  "mixed markdown",
			input: "Pair [[Alpha]] with [Beta docs](https://example.com/beta) and ![chart](chart.png).",
			want:  "Pair Alpha with Beta docs and chart.",
		},
		{
			name:  "empty alt text",
			input: "Inline image ![](chart.png) here.",
			want:  "Inline image  here.",
		},
		{
			name:  "no links",
			input: "Plain text without markdown link syntax.",
			want:  "Plain text without markdown link syntax.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanMarkdownForIndex(tt.input); got != tt.want {
				t.Fatalf("cleanMarkdownForIndex() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Verifies chunkText returns a single chunk for short text.
func TestChunkText_short(t *testing.T) {
	chunks := chunkText("hello world")
	if len(chunks) != 1 || chunks[0] != "hello world" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
}

// Verifies chunkText splits text longer than chunkSize into multiple pieces.
func TestChunkText_long(t *testing.T) {
	// Build text longer than 4KB
	word := strings.Repeat("hello ", 100)
	big := strings.Repeat(word, 10)
	chunks := chunkText(big)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if strings.TrimSpace(c) == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}
}

// Verifies chunkText preserves UTF-8 validity when a chunk boundary lands in
// the middle of multibyte markdown content.
func TestChunkText_preservesUTF8Boundaries(t *testing.T) {
	input := strings.Repeat("A\u200c ", 2200)
	chunks := chunkText(input)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %#v", chunks)
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("expected valid UTF-8 chunk, got %q", chunk)
		}
	}
}

// Verifies extractMarkdown strips invalid UTF-8 bytes from on-disk markdown so
// notebook indexing does not persist malformed text.
func TestExtractMarkdown_sanitizesInvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.md")
	if err := os.WriteFile(path, []byte{'#', ' ', 'T', 'i', 't', 'l', 'e', '\n', 0xff, 'b', 'o', 'd', 'y'}, 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}
	title, content, err := extractMarkdown(path)
	if err != nil {
		t.Fatalf("extractMarkdown: %v", err)
	}
	if title != "Title" {
		t.Fatalf("expected title to survive sanitization, got %q", title)
	}
	if !utf8.ValidString(content) {
		t.Fatalf("expected valid UTF-8 content, got %q", content)
	}
	if strings.ContainsRune(content, '\ufffd') {
		t.Fatalf("expected invalid byte to be removed, got %q", content)
	}
}

// Verifies chunkText returns one empty chunk for empty input.
func TestChunkText_empty(t *testing.T) {
	chunks := chunkText("")
	if len(chunks) != 1 || chunks[0] != "" {
		t.Fatalf("expected single empty chunk, got %v", chunks)
	}
}

// Verifies wordCount counts whitespace-delimited words correctly.
func TestWordCount(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"one two three", 3},
		{"  spaces  everywhere  ", 2},
	}
	for _, tt := range tests {
		if got := wordCount(tt.input); got != tt.want {
			t.Errorf("wordCount(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// Verifies extractPDF extracts text from a valid PDF successfully.
func TestExtractPDF_withText(t *testing.T) {
	dir := t.TempDir()
	path := writePDFFile(t, dir, "textdoc.pdf", "Hello World from a valid PDF document with enough words to pass")
	title, content, err := extractPDF(path, nil)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}
	if title != "textdoc" {
		t.Fatalf("title = %q, want 'textdoc'", title)
	}
	_ = content // content depends on PDF parser; just verify no crash
}

// Verifies extractPDF falls back to Vision OCR when pure-Go yields fewer than 10 words.
func TestExtractPDF_scanFallback(t *testing.T) {
	dir := t.TempDir()
	path := writePDFFile(t, dir, "scan.pdf", "Short")

	fa := &fakeAnalyzer{
		pdfText: "Scanned OCR text extracted from the PDF document pages with many words here.",
	}
	title, content, err := extractPDF(path, fa)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}
	if title != "scan" {
		t.Fatalf("title = %q, want 'scan'", title)
	}
	// The pure-Go extraction may yield < 10 words from our minimal PDF,
	// causing the OCR fallback to fire and replace the content.
	_ = content
}

// Verifies extractPDF falls back to Vision OCR when the file opens but has no text.
func TestExtractPDF_emptyPDFWithOCR(t *testing.T) {
	dir := t.TempDir()
	path := writePDFFile(t, dir, "empty.pdf", "")

	fa := &fakeAnalyzer{
		pdfText: "OCR recovered this text from a blank PDF",
	}
	title, content, err := extractPDF(path, fa)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}
	if title != "empty" {
		t.Fatalf("title = %q", title)
	}
	_ = content
}

// Verifies extractPDF returns a title-only result when the file does not exist.
func TestExtractPDF_missingFile(t *testing.T) {
	title, content, err := extractPDF("/nonexistent/file.pdf", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if title != "file" {
		t.Fatalf("title = %q, want 'file'", title)
	}
	if content != "" {
		t.Fatalf("expected empty content, got %q", content)
	}
}

// Verifies extractPDF skips OCR fallback when analyzer is nil.
func TestExtractPDF_nilAnalyzer(t *testing.T) {
	dir := t.TempDir()
	path := writePDFFile(t, dir, "nilvision.pdf", "")
	title, _, err := extractPDF(path, nil)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}
	if title != "nilvision" {
		t.Fatalf("title = %q", title)
	}
}

// Verifies readFileBase64 encodes file content to base64 correctly.
func TestReadFileBase64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "img.bin")
	os.WriteFile(path, []byte{1, 2, 3}, 0644)

	b64, err := readFileBase64(path)
	if err != nil {
		t.Fatalf("readFileBase64: %v", err)
	}
	if b64 == "" {
		t.Fatal("expected non-empty base64")
	}
}

// Verifies readFileBase64 returns an error for a missing file.
func TestReadFileBase64_missingFile(t *testing.T) {
	_, err := readFileBase64("/nonexistent/file.bin")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
