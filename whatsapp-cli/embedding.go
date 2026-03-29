package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
func (e *Embedder) EmbedTexts(texts []string, batchSize int) ([][]float32, error) {
	if batchSize <= 0 {
		batchSize = 64
	}
	return e.model.PassageEmbed(texts, batchSize)
}

// EmbedQuery generates a single embedding for a search query.
func (e *Embedder) EmbedQuery(query string) ([]float32, error) {
	return e.model.QueryEmbed(query)
}

// Close releases the underlying model resources.
func (e *Embedder) Close() {
	if e.model != nil {
		e.model.Destroy()
	}
}
