package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

// runMcp starts the stdio MCP server, registering tools for available authenticated sources and global search.
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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer embedder.Close()

	searchStore, err := NewSearchStore(dir, embedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: search index unavailable: %v\n", err)
	}
	if searchStore != nil {
		defer searchStore.Close()
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
