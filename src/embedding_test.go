package main

import (
	"os"
	"testing"
)

func TestNewEmbedder_noONNX(t *testing.T) {
	// Without ONNX installed, NewEmbedder should return nil, nil
	orig := os.Getenv("ONNX_PATH")
	defer os.Setenv("ONNX_PATH", orig)

	emb, err := NewEmbedder(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if emb != nil {
		emb.Close()
		t.Fatal("expected nil embedder when ONNX is not installed")
	}
}

func TestOnnxLibPath(t *testing.T) {
	path := onnxLibPath()
	if path == "" {
		t.Error("expected non-empty lib path")
	}
	// For macOS, should be one of the Homebrew paths
	if path != "/opt/homebrew/lib/libonnxruntime.dylib" && path != "/usr/local/lib/libonnxruntime.dylib" {
		t.Errorf("unexpected lib path: %s", path)
	}
}

func TestMockEmbedder_implements_interface(t *testing.T) {
	var _ EmbedderInterface = (*mockEmbedder)(nil)
}

func TestMockEmbedder_deterministic(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	v1, _ := emb.EmbedQuery("hello world")
	v2, _ := emb.EmbedQuery("hello world")
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("non-deterministic at index %d: %f != %f", i, v1[i], v2[i])
		}
	}
}

func TestMockEmbedder_batchConsistency(t *testing.T) {
	emb := &mockEmbedder{dim: 8}
	texts := []string{"hello", "world"}
	batch, err := emb.EmbedTexts(texts, 1)
	if err != nil {
		t.Fatalf("EmbedTexts: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(batch))
	}
	single, _ := emb.EmbedQuery("hello")
	for i := range single {
		if single[i] != batch[0][i] {
			t.Errorf("batch and single differ at %d", i)
		}
	}
}
