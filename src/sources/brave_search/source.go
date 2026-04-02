package brave_search

import (
	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
)

// init registers the brave_search source name so config normalization keeps a stable entry for it.
func init() {
	core.RegisterKnownSource("brave_search")
}

// Source implements core.DataSource for live Brave Search lookups.
type Source struct {
	client *BraveClient
}

// IsConfigured reports whether the build included a Brave Search API key.
func IsConfigured() bool {
	return BraveAPIKey != ""
}

// Name returns the source key used for registry lookup and tool prefixes.
func (s *Source) Name() string { return "brave_search" }

// Description returns the human label shown in CLI and status output.
func (s *Source) Description() string { return "Brave Search" }

// Close is a no-op because the live client owns no persistent local resources.
func (s *Source) Close() error { return nil }

// SearchEntries returns no index entries because Brave Search stays live-only and is never globally indexed.
func (s *Source) SearchEntries() ([]core.SearchEntry, error) {
	return nil, nil
}

// Reset is a no-op because this source owns no local files.
func (s *Source) Reset(_ string) error {
	return nil
}

// RegisterTools exposes the source's live-only search tools to MCP.
func (s *Source) RegisterTools(srv *server.MCPServer) {
	registerTools(s, srv)
}
