// Package core provides shared interfaces and helpers for CLI, MCP, daemon, and data sources.
package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

// DataSource is the required interface for every data source plugin.
// Each source owns its lifecycle and exposes MCP tools prefixed with Name()+"_".
type DataSource interface {
	// Name returns a short lowercase identifier used as a tool name prefix.
	Name() string
	// Description returns a human-readable label for the source.
	Description() string
	// RegisterTools adds the source's MCP tools to the server.
	RegisterTools(s *server.MCPServer)
	// SearchEntries returns all indexable content for the global search index.
	SearchEntries() ([]SearchEntry, error)
	// Reset removes all data files owned by this source.
	// The daemon calls this after stopping StartCore(); the CLI calls it
	// directly only when the daemon is not running.
	Reset(dataDir string) error
	// Close releases any held resources (DB connections, etc.).
	Close() error
}

//revive:disable:exported
// CoreService is implemented by data sources that run background sync (subscription or polling, max 5-minute interval).
type CoreService interface {
	// StartCore runs the source's background sync service. It blocks until ctx
	// is cancelled. Sources must use core.RunPollLoop or manage their own loop.
	StartCore(ctx context.Context) error
	// RequiresAuth returns true if this source needs credentials before running.
	RequiresAuth() bool
}

//revive:enable:exported

// SearchEntry is the unit of indexable content from any DataSource.
type SearchEntry struct {
	Source      string          `json:"source"`
	SourceID    string          `json:"source_id"`
	ContentType string          `json:"content_type"`
	Title       string          `json:"title"`
	Content     string          `json:"content"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Timestamp   *time.Time      `json:"timestamp,omitempty"`
}

// Config is the daemon config stored at {DataDir()}/config.json.
type Config struct {
	Sources map[string]SourceConfig `json:"sources"`
}

// SourceConfig holds per-source state written by CLI commands and read by the daemon.
type SourceConfig struct {
	Enabled bool            `json:"enabled"`
	Reset   bool            `json:"reset,omitempty"`
	Auth    json.RawMessage `json:"auth,omitempty"`
}
