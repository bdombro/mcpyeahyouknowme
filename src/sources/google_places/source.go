package google_places

import (
	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
)

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

func (g *Source) Name() string        { return "google_places" }
func (g *Source) Description() string { return "Google Places" }
func (g *Source) Close() error        { return nil }
func (g *Source) SearchEntries() ([]core.SearchEntry, error) {
	return nil, nil
}

// Reset is a no-op because this source owns no local files.
func (g *Source) Reset(_ string) error {
	return nil
}

// RegisterTools adds the source's live lookup tools to the MCP server.
func (g *Source) RegisterTools(s *server.MCPServer) {
	registerTools(g, s)
}
