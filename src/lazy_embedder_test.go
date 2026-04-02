package main

import (
	"errors"
	"testing"
)

type stubEmbedder struct {
	query []float32
	texts [][]float32
}

// Returns stub passage embeddings so lazy-init tests can verify delegation without loading the real model.
func (s *stubEmbedder) EmbedTexts(_ []string, _ int) ([][]float32, error) {
	return s.texts, nil
}

// Returns a stub query embedding so lazy-init tests can assert first-use initialization behavior deterministically.
func (s *stubEmbedder) EmbedQuery(_ string) ([]float32, error) {
	return s.query, nil
}

// Releases nothing because the stub embedder exists only to satisfy the production interface in tests.
func (s *stubEmbedder) Close() {}

// Verifies lazy initialization stays deferred until the first embedding call so MCP startup remains cheap.
func TestLazyEmbedder_defersInit(t *testing.T) {
	calls := 0
	lazy := newLazyEmbedderWithFactory(t.TempDir(), func(_ string) (EmbedderInterface, error) {
		calls++
		return &stubEmbedder{
			query: []float32{1, 2, 3},
			texts: [][]float32{{4, 5, 6}},
		}, nil
	})

	if calls != 0 {
		t.Fatalf("expected no eager initialization, got %d calls", calls)
	}

	queryVec, err := lazy.EmbedQuery("hello")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one initialization call after first use, got %d", calls)
	}
	if len(queryVec) != 3 {
		t.Fatalf("expected query vector length 3, got %d", len(queryVec))
	}

	textVecs, err := lazy.EmbedTexts([]string{"hello"}, 1)
	if err != nil {
		t.Fatalf("EmbedTexts: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected lazy embedder to initialize once, got %d calls", calls)
	}
	if len(textVecs) != 1 {
		t.Fatalf("expected one text vector, got %d", len(textVecs))
	}
}

// Verifies initialization failures are memoized so repeated search calls return the same error without retry loops.
func TestLazyEmbedder_initError(t *testing.T) {
	calls := 0
	lazy := newLazyEmbedderWithFactory(t.TempDir(), func(_ string) (EmbedderInterface, error) {
		calls++
		return nil, errors.New("boom")
	})

	_, err := lazy.EmbedQuery("hello")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected init error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one initialization attempt, got %d", calls)
	}

	_, err = lazy.EmbedQuery("hello again")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected cached init error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected failed init to be memoized, got %d calls", calls)
	}
}
