package main

import (
	"fmt"
	"log/slog"
	"os"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
	"mcpyeahyouknowme/sources/whatsapp"

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
			slog.Info("source unavailable, MCP tools skipped", "source", desc.Name, "reason", reason)
			continue
		}

		sc := cfg.Sources[desc.Name]
		enabled := sc.Enabled || (!desc.RunsCore && !desc.IndexGlobally)
		if !enabled {
			slog.Info("source disabled, MCP tools skipped", "source", desc.Name)
			continue
		}
		if desc.IsAuthenticated != nil && !desc.IsAuthenticated(dir) {
			slog.Info("source not authenticated, MCP tools skipped", "source", desc.Name,
				"hint", fmt.Sprintf("run 'mcpyeahyouknowme %s login' to authenticate", desc.Name))
			continue
		}
		activeSources = append(activeSources, activeSource{desc: desc, src: desc.New(dir)})
	}
	defer func() {
		for _, active := range activeSources {
			active.src.Close()
		}
	}()

	searchStore, err := NewSearchStore(dir)
	if err != nil {
		slog.Warn("search index unavailable", "err", err)
	}
	if searchStore != nil {
		defer searchStore.Close()
	}

	s := server.NewMCPServer(
		"mcpyeahyouknowme",
		BuildVersion,
		server.WithToolCapabilities(false),
	)

	adder := core.NewSecureToolAdder(s, cfg.Mcp, dir)
	for _, active := range activeSources {
		if ws, ok := active.src.(*whatsapp.Source); ok {
			ws.SetSendMessageMaxRunes(cfg.Mcp.EffectiveWhatsAppSendMaxRunes())
		}
		active.src.RegisterTools(adder)
	}

	if searchStore != nil {
		RegisterSearchTool(adder, searchStore)
		RegisterProfileTool(adder, searchStore)
	}

	restoreStderr()

	if err := server.ServeStdio(s); err != nil {
		slog.Error("MCP server error", "err", err)
		os.Exit(1)
	}
}
