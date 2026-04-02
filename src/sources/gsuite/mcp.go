package gsuite

import (
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools adds MCP read tools for each enabled app so startup exposes only the GSuite surfaces the user selected.
func (g *Source) RegisterTools(s *server.MCPServer) {
	prefix := g.Name() + "_"
	for _, app := range allApps {
		if g.apps.IsEnabled(app.name) {
			app.registerTools(g, prefix, s)
		}
	}
}
