package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

func runMcp() {
	dir := core.DataDir()

	// Only expose source tools when the source has usable credentials.
	var enabledSources []core.DataSource
	for _, desc := range registry.All {
		if desc.IsAuthenticated != nil && !desc.IsAuthenticated(dir) {
			fmt.Fprintf(os.Stderr, "Info: %s not logged in - %s MCP tools will not be available.\n", desc.Name, desc.Name)
			fmt.Fprintf(os.Stderr, "      Run 'mcpyeahyouknowme %s login' to enable %s integration.\n", desc.Name, desc.Name)
			continue
		}
		enabledSources = append(enabledSources, desc.New(dir))
	}
	defer func() {
		for _, src := range enabledSources {
			src.Close()
		}
	}()

	embedder, err := NewEmbedder(filepath.Join(dir, "models"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: embedding init failed: %v (falling back to BM25-only)\n", err)
		embedder = nil
	}
	if embedder == nil {
		fmt.Fprintf(os.Stderr, "Info: ONNX Runtime not found; semantic search disabled. Run 'brew install onnxruntime' to enable.\n")
	}

	var indexEmbedder EmbedderInterface
	if os.Getenv("MCP_ENABLE_EMBEDDINGS") == "1" {
		indexEmbedder = embedder
	} else {
		if embedder != nil {
			fmt.Fprintf(os.Stderr, "Info: Embeddings disabled during indexing (use MCP_ENABLE_EMBEDDINGS=1 to enable)\n")
		}
		indexEmbedder = nil
	}
	if embedder != nil {
		defer embedder.Close()
	}

	searchStore, err := NewSearchStore(dir, indexEmbedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: search index unavailable: %v\n", err)
	}
	if searchStore != nil {
		defer searchStore.Close()
		indexSources(searchStore, enabledSources)
	}

	s := server.NewMCPServer(
		"mcpyeahyouknowme",
		BuildVersion,
		server.WithToolCapabilities(false),
	)

	for _, src := range enabledSources {
		src.RegisterTools(s)
	}

	if searchStore != nil {
		RegisterSearchTool(s, searchStore)
	}

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

// indexSources populates the search index from all data sources.
func indexSources(store *SearchStore, sources []core.DataSource) {
	for _, src := range sources {
		entries, err := src.SearchEntries()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get search entries from %s: %v\n", src.Name(), err)
			continue
		}
		if err := store.IndexEntries(entries); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to index %s entries: %v\n", src.Name(), err)
			continue
		}
		store.UpdateSourceTimestamp(src.Name(), time.Now())
	}
}
