package notebook

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

const chunkSize = 4096

// extractMarkdown reads a .md file and returns its title (first H1 or filename) and full content.
func extractMarkdown(path string) (title, content string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	content = string(data)
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

// chunkText splits text into ~chunkSize byte pieces on word boundaries, returning at least one chunk.
func chunkText(text string) []string {
	text = strings.TrimSpace(text)
	if len(text) == 0 {
		return []string{""}
	}
	if len(text) <= chunkSize {
		return []string{text}
	}
	var chunks []string
	for len(text) > chunkSize {
		cut := chunkSize
		for cut > chunkSize/2 && cut < len(text) && !unicode.IsSpace(rune(text[cut])) {
			cut++
		}
		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
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
