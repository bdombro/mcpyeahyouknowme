package googledocs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/mark3labs/mcp-go/mcp"
)

// newTestGoogleDocsDB opens a fresh in-memory SQLite database with the
// googledocs schema applied. The DB is closed automatically at test end.
func newTestGoogleDocsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := initGoogleDocsDB(db); err != nil {
		t.Fatalf("initGoogleDocsDB: %v", err)
	}
	return db
}

// newMCPTestSource creates a Source backed by a seeded in-memory DB.
func newMCPTestSource(t *testing.T) *Source {
	t.Helper()
	db := newTestGoogleDocsDB(t)
	_, err := db.Exec(`
		INSERT INTO documents (id, title, content, modified_time, created_time, web_view_link, last_synced)
		VALUES
		  ('doc1', 'Alpha Document', 'Hello alpha world', '2024-01-02T00:00:00Z', '2024-01-01T00:00:00Z', 'https://docs.google.com/doc1', '2024-01-02T00:00:00Z'),
		  ('doc2', 'Beta Document',  'Beta content here', '2024-01-03T00:00:00Z', '2024-01-01T00:00:00Z', 'https://docs.google.com/doc2', '2024-01-03T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("seed documents: %v", err)
	}
	return &Source{db: db, dataDir: t.TempDir()}
}

// buildMCPRequest constructs a CallToolRequest with the given arguments map.
func buildMCPRequest(args map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	}
}

// extractResultText returns the JSON-encoded text of the first TextContent block.
func extractResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			data, _ := json.Marshal(tc.Text)
			return string(data)
		}
	}
	return ""
}

// callHandler is a thin helper for calling a bound handler method and
// unwrapping the first content text, for success-path assertions.
func callHandler(t *testing.T, fn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]interface{}) (string, bool) {
	t.Helper()
	result, err := fn(context.Background(), buildMCPRequest(args))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	return extractResultText(t, result), result.IsError
}
