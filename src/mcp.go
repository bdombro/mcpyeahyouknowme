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

type activeSource struct {
	desc registry.Descriptor
	src  core.DataSource
}

type sourceIndexer interface {
	IndexEntries(entries []core.SearchEntry) error
	UpdateSourceTimestamp(source string, ts time.Time)
}

func runMcp() {
	dir := core.DataDir()
	cfg := core.LoadConfig(dir)

	var activeSources []activeSource
	for _, desc := range registry.All {
		available, reason := registry.IsAvailable(desc.Name)
		if !available {
			fmt.Fprintf(os.Stderr, "Info: %s is unavailable - %s MCP tools will not be available.\n", desc.Name, desc.Name)
			if reason != "" {
				fmt.Fprintf(os.Stderr, "      %s.\n", reason)
			}
			fmt.Fprintf(os.Stderr, "      Rebuild with the required credentials to enable it.\n")
			continue
		}

		sc := cfg.Sources[desc.Name]
		enabled := sc.Enabled || (!desc.RunsCore && !desc.IndexGlobally)
		if !enabled {
			fmt.Fprintf(os.Stderr, "Info: %s is disabled - %s MCP tools will not be available.\n", desc.Name, desc.Name)
			fmt.Fprintf(os.Stderr, "      Enable it by logging in again or updating config.json.\n")
			continue
		}
		if desc.IsAuthenticated != nil && !desc.IsAuthenticated(dir) {
			fmt.Fprintf(os.Stderr, "Info: %s is enabled but not authenticated - %s MCP tools will not be available.\n", desc.Name, desc.Name)
			fmt.Fprintf(os.Stderr, "      Run 'mcpyeahyouknowme %s login' to authenticate %s.\n", desc.Name, desc.Name)
			continue
		}
		activeSources = append(activeSources, activeSource{desc: desc, src: desc.New(dir)})
	}
	defer func() {
		for _, active := range activeSources {
			active.src.Close()
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
		indexSources(searchStore, activeSources)
	}

	s := server.NewMCPServer(
		"mcpyeahyouknowme",
		BuildVersion,
		server.WithToolCapabilities(false),
	)

	for _, active := range activeSources {
		active.src.RegisterTools(s)
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
func indexSources(store sourceIndexer, sources []activeSource) {
	for _, active := range sources {
		if !active.desc.IndexGlobally {
			continue
		}
		entries, err := active.src.SearchEntries()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get search entries from %s: %v\n", active.src.Name(), err)
			continue
		}
		if err := store.IndexEntries(entries); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to index %s entries: %v\n", active.src.Name(), err)
			continue
		}
		store.UpdateSourceTimestamp(active.src.Name(), time.Now())
	}
}
