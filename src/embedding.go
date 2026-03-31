package main

import (
	"fmt"
	"strings"

	fastembed "github.com/bdombro/fastembed-go"
)

// Embedder wraps fastembed-go for generating text embeddings.
// Implements EmbedderInterface.
type Embedder struct {
	model *fastembed.FlagEmbedding
}

// EmbedTexts generates embeddings for a batch of texts using passage mode.
// Filters out empty/whitespace-only strings to prevent tokenizer crashes.
func (e *Embedder) EmbedTexts(texts []string, batchSize int) ([][]float32, error) {
	if batchSize <= 0 {
		batchSize = 64
	}

	var validTexts []string
	var validIndices []int
	for i, text := range texts {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" && len(trimmed) > 0 {
			validTexts = append(validTexts, trimmed)
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
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	return e.safeQueryEmbed(trimmed)
}

// Close releases the underlying model resources.
func (e *Embedder) Close() {
	if e.model != nil {
		e.model.Destroy()
	}
}
