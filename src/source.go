package main

import (
	"context"

	"github.com/mark3labs/mcp-go/server"
)

// DataSource represents a pluggable data backend that registers its own MCP
// tools. Each source owns its lifecycle (DB connections, HTTP clients) and
// exposes tools namespaced under its Name() prefix (e.g. "whatsapp_list_chats").
type DataSource interface {
	// Name returns a short identifier used as a tool name prefix.
	// Must be lowercase, alphanumeric, no spaces (e.g. "whatsapp", "gmail", "gdrive").
	Name() string

	// Description returns a human-readable label for the source (e.g. "WhatsApp").
	Description() string

	// RegisterTools adds the source's MCP tools to the server.
	// Tool names must be prefixed with Name() + "_".
	RegisterTools(s *server.MCPServer)

	// SearchEntries returns all indexable content from this source for the
	// global search index. Content types include "chat_name", "participant",
	// and "message".
	SearchEntries() ([]SearchEntry, error)

	// Close releases any resources held by the source.
	Close() error
}

// CoreService is an optional interface for data sources that need to run
// persistent background services (e.g., maintaining active connections,
// syncing data). Sources that only provide MCP tools don't need to implement this.
type CoreService interface {
	// StartCore runs the source's background services. This should block
	// until interrupted via context cancellation. If the source needs
	// authentication, it should check that before starting.
	StartCore(ctx context.Context) error

	// RequiresAuth returns true if this source needs authentication before
	// running core services. If true, the daemon will skip this source if
	// the user hasn't logged in.
	RequiresAuth() bool
}

// LoadSources returns all enabled data sources. Currently only WhatsApp.
// Future sources (Gmail, Google Drive, etc.) will be added here.
func LoadSources() ([]DataSource, error) {
	var sources []DataSource

	ws, err := NewWhatsAppSource()
	if err != nil {
		return nil, err
	}
	sources = append(sources, ws)

	return sources, nil
}
