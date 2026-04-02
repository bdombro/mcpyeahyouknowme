package main

import (
	"fmt"
	"sync"
)

type embedderFactory func(cacheDir string) (EmbedderInterface, error)

// LazyEmbedder defers expensive model initialization until embeddings are used.
type LazyEmbedder struct {
	cacheDir string
	factory  embedderFactory

	once sync.Once
	emb  EmbedderInterface
	err  error
}

// Returns a lazily initialized embedder so MCP startup avoids paying model-load cost before semantic search is used.
func NewLazyEmbedder(cacheDir string) *LazyEmbedder {
	return newLazyEmbedderWithFactory(cacheDir, func(cacheDir string) (EmbedderInterface, error) {
		return NewEmbedder(cacheDir)
	})
}

// Builds a lazy embedder with an injected factory so tests can control initialization success, failure, and call counts.
func newLazyEmbedderWithFactory(cacheDir string, factory embedderFactory) *LazyEmbedder {
	return &LazyEmbedder{
		cacheDir: cacheDir,
		factory:  factory,
	}
}

// Loads the underlying embedder at most once, memoizing both success and failure so repeated MCP calls stay deterministic.
func (l *LazyEmbedder) ensureLoaded() (EmbedderInterface, error) {
	l.once.Do(func() {
		if l.factory == nil {
			l.err = fmt.Errorf("embedder factory is not configured")
			return
		}
		l.emb, l.err = l.factory(l.cacheDir)
		if l.err == nil && l.emb == nil {
			l.err = fmt.Errorf("embedder factory returned nil embedder")
		}
	})
	if l.err != nil {
		return nil, l.err
	}
	return l.emb, nil
}

// Returns passage embeddings after forcing one-time model initialization, preserving the EmbedderInterface contract for search indexing callers.
func (l *LazyEmbedder) EmbedTexts(texts []string, batchSize int) ([][]float32, error) {
	emb, err := l.ensureLoaded()
	if err != nil {
		return nil, err
	}
	return emb.EmbedTexts(texts, batchSize)
}

// Returns a query embedding after forcing one-time model initialization so MCP search pays the ONNX cost only on first semantic use.
func (l *LazyEmbedder) EmbedQuery(query string) ([]float32, error) {
	emb, err := l.ensureLoaded()
	if err != nil {
		return nil, err
	}
	return emb.EmbedQuery(query)
}

// Releases the underlying embedder if initialization ever succeeded, and otherwise remains a no-op for unopened lazy instances.
func (l *LazyEmbedder) Close() {
	if l.emb != nil {
		l.emb.Close()
	}
}
