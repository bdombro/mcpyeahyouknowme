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

// CoreService is implemented by data sources that run background sync (subscription or polling, max 5-minute interval).
//
//revive:disable:exported
type CoreService interface {
	// StartCore runs the source's background sync service. It blocks until ctx
	// is cancelled. Sources must use core.RunPollLoop or manage their own loop.
	StartCore(ctx context.Context) error
	// RequiresAuth returns true if this source needs credentials before running.
	RequiresAuth() bool
}

// StreamingSource is implemented by data sources that can emit search rows in
// batches so daemon indexing avoids materializing the full corpus in memory.
type StreamingSource interface {
	// StreamSearchEntries emits zero or more batches of SearchEntry values. The
	// callback may return an error to stop streaming early.
	StreamSearchEntries(emit func([]SearchEntry) error) error
}

// IncrementalSource is implemented by data sources that can cheaply decide
// whether their indexed content changed since the last successful pass.
type IncrementalSource interface {
	// HasChangesSince reports whether the source should be re-indexed after t.
	// A zero time means no previous successful index watermark exists.
	HasChangesSince(t time.Time) bool
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
