package main

import (
	"fmt"
	"os"

	fastembed "github.com/bdombro/fastembed-go"
)

// onnxLibPath returns the Homebrew-installed ONNX runtime library path for macOS.
// Checks both Apple Silicon (/opt/homebrew) and Intel (/usr/local) paths.
func onnxLibPath() string {
	armPath := "/opt/homebrew/lib/libonnxruntime.dylib"
	if _, err := os.Stat(armPath); err == nil {
		return armPath
	}
	return "/usr/local/lib/libonnxruntime.dylib"
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

// safePassageEmbed wraps model.PassageEmbed with panic recovery for the
// ONNX runtime which can crash across goroutine boundaries.
func (e *Embedder) safePassageEmbed(texts []string, batchSize int) (result [][]float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("embedding panic: %v", r)
		}
	}()
	return e.model.PassageEmbed(texts, batchSize)
}

// safeQueryEmbed wraps model.QueryEmbed with panic recovery.
func (e *Embedder) safeQueryEmbed(query string) (result []float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("embedding panic: %v", r)
		}
	}()
	return e.model.QueryEmbed(query)
}
