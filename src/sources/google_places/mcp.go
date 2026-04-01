package google_places

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTools(src *Source, s *server.MCPServer) {
	prefix := src.Name() + "_"

	s.AddTool(mcp.NewTool(prefix+"search_places",
		mcp.WithDescription("Search for a business or address using the Google Places API."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Business name, address, or other place text to search for")),
		mcp.WithNumber("max_results", mcp.Description("Maximum number of matching places to return (default 5, max 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		maxResults := core.IntArg(req.GetArguments(), "max_results", 5)
		results, err := src.client.SearchPlaces(ctx, query, maxResults)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(results)
	})

	s.AddTool(mcp.NewTool(prefix+"get_place",
		mcp.WithDescription("Get detailed information for a Google Place by place_id."),
		mcp.WithString("place_id", mcp.Required(), mcp.Description("The place_id returned by google_places_search_places")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		placeID, _ := req.RequireString("place_id")
		result, err := src.client.GetPlace(ctx, placeID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(result)
	})
}
