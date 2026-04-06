package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"

	"github.com/mark3labs/mcp-go/server"
)

type stderrOpenFunc func() (*os.File, error)

// Suppresses non-fatal stderr output during MCP startup so hosts that treat any stderr as a hard error still keep the server connected.
func suppressStderr() func() {
	return suppressStderrWithOpen(func() (*os.File, error) {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	})
}

// Suppresses non-fatal stderr output using an injected writer opener so tests can cover both the happy path and fallback path deterministically.
func suppressStderrWithOpen(openDevNull stderrOpenFunc) func() {
	original := os.Stderr
	devNull, err := openDevNull()
	if err != nil {
		return func() {}
	}
	os.Stderr = devNull
	return func() {
		os.Stderr = original
		devNull.Close()
	}
}

// runMcp starts the stdio MCP server, registering tools for available authenticated sources and global search.
func runMcp() {
	restoreStderr := suppressStderr()

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

	embedder := NewLazyEmbedder(filepath.Join(dir, "models"))
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
		RegisterProfileTool(s, searchStore)
	}

	restoreStderr()

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
