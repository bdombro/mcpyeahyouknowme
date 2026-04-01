package google_places

import (
	"context"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTools(src *Source, s *server.MCPServer) {
	prefix := src.Name() + "_"

	s.AddTool(core.NewReadOnlyTool(prefix+"search_places",
		core.ToolDescription("Search for a business or address using the Google Places API.", `{"query":"Blue Bottle Coffee Oakland","max_results":3}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Business name, address, or other place text to search for")),
		mcp.WithNumber("max_results", mcp.Description("Maximum number of matching places to return (default 5, max 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, errResult := core.RequireStringArgument(req, "query", `{"query":"Blue Bottle Coffee Oakland","max_results":3}`)
		if errResult != nil {
			return errResult, nil
		}
		maxResults := core.IntArg(req.GetArguments(), "max_results", 5)
		results, err := src.client.SearchPlaces(ctx, query, maxResults)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(results)
	})

	s.AddTool(core.NewReadOnlyTool(prefix+"get_place",
		core.ToolDescription("Get detailed information for a Google Place by place_id.", `{"place_id":"ChIJ..."}`),
		mcp.WithString("place_id", mcp.Required(), mcp.Description("The place_id returned by google_places_search_places")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		placeID, errResult := core.RequireStringArgument(req, "place_id", `{"place_id":"ChIJ..."}`)
		if errResult != nil {
			return errResult, nil
		}
		result, err := src.client.GetPlace(ctx, placeID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(result)
	})
}
