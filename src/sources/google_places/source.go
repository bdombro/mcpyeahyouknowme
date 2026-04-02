package google_places

import (
	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
)

// init registers the google_places source name so config normalization keeps a stable entry for it.
func init() {
	core.RegisterKnownSource("google_places")
}

// Source implements core.DataSource for live Google Places lookups.
type Source struct {
	client *PlacesClient
}

// IsConfigured reports whether the build included a Places API key.
func IsConfigured() bool {
	return GooglePlaceAPIKey != ""
}

// Name returns the source key used for registry lookup and tool prefixes.
func (g *Source) Name() string        { return "google_places" }
// Description returns the human label shown in CLI and status output.
func (g *Source) Description() string { return "Google Places" }
// Close is a no-op because the live client owns no persistent local resources.
func (g *Source) Close() error        { return nil }
// SearchEntries returns no index entries because Places stays live-only and is never globally indexed.
func (g *Source) SearchEntries() ([]core.SearchEntry, error) {
	return nil, nil
}

// Reset is a no-op because this source owns no local files.
func (g *Source) Reset(_ string) error {
	return nil
}

// RegisterTools exposes the source's live-only lookup tools to MCP so clients can call Places without any local cache or sync step.
func (g *Source) RegisterTools(s *server.MCPServer) {
	registerTools(g, s)
}
