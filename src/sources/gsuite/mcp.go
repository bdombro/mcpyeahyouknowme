package gsuite

import (
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools adds all enabled Google Workspace MCP tools to the server.
func (g *Source) RegisterTools(s *server.MCPServer) {
	prefix := g.Name() + "_"
	for _, app := range allApps {
		if g.apps.IsEnabled(app.name) {
			app.registerTools(g, prefix, s)
		}
	}
}
