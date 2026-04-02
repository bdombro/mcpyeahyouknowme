package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------- ONNX lib path ----------

func TestOnnxLibPath(t *testing.T) {
	path := onnxLibPath()
	if path == "" {
		t.Error("expected non-empty lib path")
	}
	// For macOS, should be one of the Homebrew paths
	expectedPaths := []string{
		"/opt/homebrew/lib/libonnxruntime.dylib",
		"/usr/local/lib/libonnxruntime.dylib",
	}
	found := false
	for _, expected := range expectedPaths {
		if path == expected {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("unexpected lib path: %s (expected one of %v)", path, expectedPaths)
	}
}

// ---------- No-ONNX path ----------

func TestNewEmbedder_noONNX(t *testing.T) {
	libPath := onnxLibPath()
	if _, err := os.Stat(libPath); err == nil {
		t.Skipf("ONNX is installed at %s, skipping no-ONNX test", libPath)
	}

	emb, err := NewEmbedder(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if emb != nil {
		emb.Close()
		t.Fatal("expected nil embedder when ONNX is not installed")
	}
}

// ---------- Real ONNX embedder (shared instance) ----------

// sharedEmbedder lazily creates a single Embedder for all ONNX tests, avoiding
// repeated 66 MB model downloads. It skips the calling test when ONNX is absent
// or if the model download fails (e.g. network errors, Hugging Face 500s).
var (
	sharedEmb     *Embedder
	sharedEmbErr  error
	sharedEmbOnce sync.Once
	sharedEmbDir  string // persists across tests in the same process
)

// stableCacheDir returns a persistent directory for the ONNX model so it is
// downloaded once and reused across all future test runs.
func stableCacheDir() string {
	base, _ := os.UserCacheDir()
	if base == "" {
		base = os.TempDir()
	}
	d := filepath.Join(base, "mcpyeahyouknowme-test-embedder")
	os.MkdirAll(d, 0o755)
	return d
}

func getSharedEmbedder(t *testing.T) *Embedder {
	t.Helper()

	libPath := onnxLibPath()
	if _, err := os.Stat(libPath); err != nil {
		t.Skipf("ONNX not installed at %s, skipping", libPath)
	}

	sharedEmbOnce.Do(func() {
		sharedEmbDir = stableCacheDir()
		sharedEmb, sharedEmbErr = NewEmbedder(sharedEmbDir)
	})

	if sharedEmbErr != nil {
		t.Skipf("Could not create embedder (network/model error): %v", sharedEmbErr)
	}
	if sharedEmb == nil {
		t.Skip("Embedder is nil despite ONNX being present")
	}

	return sharedEmb
}

func TestEmbedder_RealONNX(t *testing.T) {
	emb := getSharedEmbedder(t)

	t.Run("implements interface", func(_ *testing.T) {
		var _ EmbedderInterface = emb
	})

	t.Run("EmbedQuery", func(t *testing.T) {
		tests := []struct {
			name      string
			query     string
			expectErr bool
		}{
			{"simple text", "hello world", false},
			{"longer text", "This is a longer test sentence with more words", false},
			{"empty string", "", true},
			{"whitespace only", "   ", true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				vec, err := emb.EmbedQuery(tt.query)
				if tt.expectErr {
					if err == nil {
						t.Error("expected error for invalid input")
					}
					return
				}
				if err != nil {
					t.Fatalf("EmbedQuery: %v", err)
				}
				if len(vec) == 0 {
					t.Error("expected non-empty embedding vector")
				}
			})
		}
	})

	t.Run("EmbedTexts", func(t *testing.T) {
		tests := []struct {
			name        string
			texts       []string
			batchSize   int
			expectedLen int
		}{
			{"single text", []string{"hello"}, 1, 1},
			{"multiple texts", []string{"hello", "world", "test"}, 2, 3},
			{"with empty strings", []string{"hello", "", "world"}, 1, 3},
			{"all empty", []string{"", "  ", "   "}, 1, 3},
			{"default batch size", []string{"test1", "test2"}, 0, 2},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				vecs, err := emb.EmbedTexts(tt.texts, tt.batchSize)
				if err != nil {
					t.Fatalf("EmbedTexts: %v", err)
				}
				if len(vecs) != tt.expectedLen {
					t.Errorf("expected %d vectors, got %d", tt.expectedLen, len(vecs))
				}
				for i, text := range tt.texts {
					if len(strings.TrimSpace(text)) > 0 && len(vecs[i]) == 0 {
						t.Errorf("text[%d] non-empty but got empty vector", i)
					}
				}
			})
		}
	})

	t.Run("EmbedTexts_emptyInput", func(t *testing.T) {
		vecs, err := emb.EmbedTexts([]string{}, 1)
		if err != nil {
			t.Errorf("EmbedTexts on empty array: %v", err)
		}
		if len(vecs) != 0 {
			t.Errorf("expected empty result, got %d vectors", len(vecs))
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		text := "test consistency"
		vec1, err := emb.EmbedQuery(text)
		if err != nil {
			t.Fatalf("first EmbedQuery: %v", err)
		}
		vec2, err := emb.EmbedQuery(text)
		if err != nil {
			t.Fatalf("second EmbedQuery: %v", err)
		}
		if len(vec1) != len(vec2) {
			t.Fatalf("vector lengths differ: %d vs %d", len(vec1), len(vec2))
		}
		for i := range vec1 {
			if vec1[i] != vec2[i] {
				t.Errorf("vectors differ at index %d: %f vs %f", i, vec1[i], vec2[i])
			}
		}
	})
}

// TestEmbedder_Close uses its own fresh embedder since Close invalidates it.
func TestEmbedder_Close(t *testing.T) {
	libPath := onnxLibPath()
	if _, err := os.Stat(libPath); err != nil {
		t.Skipf("ONNX not installed, skipping")
	}

	// Reuse shared cache dir so we don't re-download the model
	sharedEmbOnce.Do(func() {
		sharedEmbDir = stableCacheDir()
		sharedEmb, sharedEmbErr = NewEmbedder(sharedEmbDir)
	})
	if sharedEmbDir == "" {
		t.Skip("no shared cache dir available")
	}

	emb, err := NewEmbedder(sharedEmbDir)
	if err != nil || emb == nil {
		t.Skipf("Could not create embedder: %v", err)
	}

	// Close should not panic
	emb.Close()
	// Calling Close again should also not panic
	emb.Close()
}

// ---------- Mock embedder ----------

func TestMockEmbedder_implements_interface(_ *testing.T) {
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
