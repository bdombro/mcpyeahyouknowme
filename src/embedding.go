package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	fastembed "github.com/anush008/fastembed-go"
)

// Embedder wraps fastembed-go for generating text embeddings.
// Implements EmbedderInterface.
type Embedder struct {
	model *fastembed.FlagEmbedding
}

// onnxLibName returns the platform-specific ONNX runtime library filename.
func onnxLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime.dylib"
	case "windows":
		return "onnxruntime.dll"
	default:
		return "libonnxruntime.so"
	}
}

// onnxLibPath returns the app-local path where ONNX runtime is expected.
func onnxLibPath() string {
	return filepath.Join(dataDir(), "lib", onnxLibName())
}

// NewEmbedder creates an Embedder by looking for the ONNX runtime library
// in the app-local lib directory. Returns (nil, nil) if ONNX is not installed,
// allowing the caller to fall back to BM25-only search.
func NewEmbedder(cacheDir string) (emb *Embedder, err error) {
	libPath := onnxLibPath()
	if _, statErr := os.Stat(libPath); os.IsNotExist(statErr) {
		return nil, nil
	}

	os.Setenv("ONNX_PATH", libPath)

	// fastembed-go panics on ONNX load failure; recover gracefully.
	defer func() {
		if r := recover(); r != nil {
			emb = nil
			err = fmt.Errorf("embedding init failed: %v", r)
		}
	}()

	model, initErr := fastembed.NewFlagEmbedding(&fastembed.InitOptions{
		Model:    fastembed.BGESmallENV15,
		CacheDir: cacheDir,
	})
	if initErr != nil {
		return nil, fmt.Errorf("fastembed init: %w", initErr)
	}

	return &Embedder{model: model}, nil
}

// EmbedTexts generates embeddings for a batch of texts using passage mode.
// Filters out empty/whitespace-only strings to prevent tokenizer crashes.
// Recovers from panics in the underlying library and returns partial results.
func (e *Embedder) EmbedTexts(texts []string, batchSize int) (embeddings [][]float32, err error) {
	if batchSize <= 0 {
		batchSize = 64
	}
	
	// Recover from panics in the tokenizer library
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("embedding panic: %v", r)
			embeddings = make([][]float32, len(texts))
		}
	}()
	
	// Filter out empty texts and track original indices
	var validTexts []string
	var validIndices []int
	for i, text := range texts {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" && len(trimmed) > 0 {
			validTexts = append(validTexts, trimmed)
			validIndices = append(validIndices, i)
		}
	}
	
	// If no valid texts, return empty embeddings for all inputs
	if len(validTexts) == 0 {
		result := make([][]float32, len(texts))
		return result, nil
	}
	
	// Embed only valid texts
	validEmbeddings, embErr := e.model.PassageEmbed(validTexts, batchSize)
	if embErr != nil {
		return nil, embErr
	}
	
	// Reconstruct result array with embeddings in original positions
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
// Recovers from panics in the underlying library.
func (e *Embedder) EmbedQuery(query string) (embedding []float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("embedding panic: %v", r)
			embedding = nil
		}
	}()
	
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	return e.model.QueryEmbed(trimmed)
}

// Close releases the underlying model resources.
func (e *Embedder) Close() {
	if e.model != nil {
		e.model.Destroy()
	}
}
