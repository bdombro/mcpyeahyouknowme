package notebook

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"mcpyeahyouknowme/core"

	"github.com/ledongthuc/pdf"
)

const chunkSize = core.ChunkMaxChars

var (
	reWikiLinkAlias = regexp.MustCompile(`\[\[[^\]|]+\|([^\]]+)\]\]`)
	reWikiLink      = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	reMarkdownImage = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	reMarkdownLink  = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
)

// extractMarkdown reads a .md file and returns its title (first H1 or filename) and full content.
func extractMarkdown(path string) (title, content string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	content = strings.ToValidUTF8(string(data), "")
	title = extractMarkdownTitle(content, filepath.Base(path))
	return title, content, nil
}

// extractMarkdownTitle returns the first H1 heading from the markdown body, or the filename stem as a fallback.
func extractMarkdownTitle(content, filename string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return stemName(filename)
}

// stemName strips the file extension from a filename to produce a human-readable title.
func stemName(filename string) string {
	base := filepath.Base(filename)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// Cleans markdown link syntax before indexing so FTS keeps human-readable text without URL noise.
func cleanMarkdownForIndex(content string) string {
	content = reWikiLinkAlias.ReplaceAllString(content, "$1")
	content = reWikiLink.ReplaceAllString(content, "$1")
	content = reMarkdownImage.ReplaceAllString(content, "$1")
	content = reMarkdownLink.ReplaceAllString(content, "$1")
	return content
}

// chunkText splits text into ~chunkSize rune pieces on word boundaries, returning at least one chunk.
func chunkText(text string) []string {
	text = strings.TrimSpace(strings.ToValidUTF8(text, ""))
	if len(text) == 0 {
		return []string{""}
	}
	if utf8.RuneCountInString(text) <= chunkSize {
		return []string{text}
	}
	var chunks []string
	for utf8.RuneCountInString(text) > chunkSize {
		runes := []rune(text)
		cut := chunkSize
		for cut > chunkSize/2 && cut < len(runes) && !unicode.IsSpace(runes[cut]) {
			cut++
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[:cut])))
		text = strings.TrimSpace(string(runes[cut:]))
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}

// extractPDF attempts pure-Go text extraction; if fewer than 10 words are found, falls back to Vision OCR page rendering.
func extractPDF(path string, analyzer ImageAnalyzer) (title, content string, err error) {
	title = stemName(filepath.Base(path))

	f, reader, err := pdf.Open(path)
	if err != nil {
		return title, "", nil
	}
	defer f.Close()

	var buf bytes.Buffer
	plainText, _ := reader.GetPlainText()
	if plainText != nil {
		buf.ReadFrom(plainText)
	}
	text := strings.TrimSpace(buf.String())

	if wordCount(text) < 10 && analyzer != nil {
		ocrText, ocrErr := analyzer.OCRPDFPages(path)
		if ocrErr == nil && strings.TrimSpace(ocrText) != "" {
			text = strings.TrimSpace(ocrText)
		}
	}

	return title, text, nil
}

// wordCount returns the number of whitespace-delimited words in a string.
func wordCount(s string) int {
	return len(strings.Fields(s))
}

// readFileBase64 reads a file and returns its base64-encoded content for binary transfer.
func readFileBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
