package main

import (
	"fmt"
	"regexp"
	"strings"

	"mcpyeahyouknowme/core"

	fastembed "github.com/bdombro/fastembed-go"
)

// multiSpaceRegex matches two or more consecutive whitespace characters.
// Used to collapse runs of whitespace before tokenization to avoid the
// sugarme/tokenizer off-by-one panic on long whitespace sequences (issue #78).
var multiSpaceRegex = regexp.MustCompile(`\s{2,}`)

// Embedder wraps fastembed-go for generating text embeddings.
// Implements EmbedderInterface.
type Embedder struct {
	model *fastembed.FlagEmbedding
}

// normalizeEmbedText coerces invalid UTF-8, trims edges, collapses whitespace,
// and truncates to the model context budget so broken source rows cannot crash
// tokenizer-based embedding calls.
func normalizeEmbedText(s string) string {
	normalized := strings.ToValidUTF8(s, "")
	normalized = multiSpaceRegex.ReplaceAllString(strings.TrimSpace(normalized), " ")
	return truncateRunes(normalized, core.EmbedContextChars)
}

// truncateRunes caps a string by rune count so callers can enforce character
// budgets without splitting multibyte UTF-8 sequences.
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

// EmbedTexts generates embeddings for a batch of texts using passage mode.
// Filters out empty/whitespace-only strings and collapses consecutive whitespace
// to prevent tokenizer crashes.
func (e *Embedder) EmbedTexts(texts []string, batchSize int) ([][]float32, error) {
	if batchSize <= 0 {
		batchSize = 64
	}

	var validTexts []string
	var validIndices []int
	for i, text := range texts {
		normalized := normalizeEmbedText(text)
		if normalized != "" {
			validTexts = append(validTexts, normalized)
			validIndices = append(validIndices, i)
		}
	}

	if len(validTexts) == 0 {
		return make([][]float32, len(texts)), nil
	}

	validEmbeddings, embErr := e.safePassageEmbed(validTexts, batchSize)
	if embErr != nil { // nocov
		return nil, embErr
	}

	result := make([][]float32, len(texts))
	for i, validIdx := range validIndices {
		if i < len(validEmbeddings) {
			result[validIdx] = validEmbeddings[i]
		}
	}

	return result, nil
}

// EmbedQuery generates a single embedding for a search query.
// Returns error if query is empty or whitespace-only.
func (e *Embedder) EmbedQuery(query string) ([]float32, error) {
	normalized := normalizeEmbedText(query)
	if normalized == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	return e.safeQueryEmbed(normalized)
}

// Close releases the underlying model resources.
func (e *Embedder) Close() {
	if e.model != nil {
		e.model.Destroy()
	}
}
