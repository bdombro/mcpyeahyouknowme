package notebook

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	_ "modernc.org/sqlite"
)

// fakeAnalyzer is a test double for ImageAnalyzer that returns configurable results without calling Vision.
type fakeAnalyzer struct {
	ocrText string
	labels  []string
	ocrErr  error
	pdfText string
	pdfErr  error
}

// AnalyzeImage returns the pre-configured OCR text and labels for test assertions.
func (f *fakeAnalyzer) AnalyzeImage(_ string) (string, []string, error) {
	return f.ocrText, f.labels, f.ocrErr
}

// OCRPDFPages returns the pre-configured PDF OCR text for test assertions.
func (f *fakeAnalyzer) OCRPDFPages(_ string) (string, error) {
	return f.pdfText, f.pdfErr
}

// Builds an in-memory SQLite DB with the notebook schema for isolated tests.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := initNotebookDB(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Builds a Source backed by an in-memory DB and a temp dataDir for isolated tests.
func newTestSource(t *testing.T, dirs []string) *Source {
	t.Helper()
	dataDir := t.TempDir()
	db := newTestDB(t)
	src := &Source{db: db, dataDir: dataDir}
	if len(dirs) > 0 {
		cfg := NotebookConfig{Dirs: dirs}
		data, _ := json.Marshal(cfg)
		saveNotebookConfig(dataDir, cfg)
		_ = data
	}
	t.Cleanup(func() { src.db = nil })
	return src
}

// Builds a minimal MCP server with notebook tools registered for MCP handler tests.
func buildMCPServer(t *testing.T, src *Source) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	return s
}

// Invokes one MCP tool through the JSON-RPC path and returns the first text payload plus the error flag.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) (string, bool) {
	t.Helper()

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	result := s.HandleMessage(context.Background(), msg)
	data, _ := json.Marshal(result)

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, string(data))
	}
	if len(resp.Result.Content) == 0 {
		return "", resp.Result.IsError
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

// Writes a markdown file to dir with content and returns its path.
func writeMDFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write md file: %v", err)
	}
	return path
}

// Writes an empty image placeholder file to dir and returns its path.
func writeImageFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte{0xFF, 0xD8, 0xFF, 0xE0}, 0644); err != nil {
		t.Fatalf("write image file: %v", err)
	}
	return path
}

// Writes a valid minimal PDF with embedded text content and returns its path.
func writePDFFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, generateTestPDF(text), 0644); err != nil {
		t.Fatalf("write pdf file: %v", err)
	}
	return path
}

// Builds a minimal valid PDF containing a text-drawing stream for extractPDF to parse.
func generateTestPDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 12 Tf 100 700 Td (%s) Tj ET", text)

	obj1 := "1 0 obj\n<</Type /Catalog /Pages 2 0 R>>\nendobj\n"
	obj2 := "2 0 obj\n<</Type /Pages /Kids [3 0 R] /Count 1>>\nendobj\n"
	obj3 := "3 0 obj\n<</Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources <</Font <</F1 5 0 R>>>>>>\nendobj\n"
	obj4 := fmt.Sprintf("4 0 obj\n<</Length %d>>\nstream\n%s\nendstream\nendobj\n", len(stream), stream)
	obj5 := "5 0 obj\n<</Type /Font /Subtype /Type1 /BaseFont /Helvetica>>\nendobj\n"

	header := "%PDF-1.4\n"
	offsets := make([]int, 6)
	pos := len(header)

	offsets[1] = pos
	pos += len(obj1)
	offsets[2] = pos
	pos += len(obj2)
	offsets[3] = pos
	pos += len(obj3)
	offsets[4] = pos
	pos += len(obj4)
	offsets[5] = pos
	pos += len(obj5)

	xrefStart := pos
	xref := "xref\n0 6\n"
	xref += fmt.Sprintf("%010d 65535 f \n", 0)
	for i := 1; i <= 5; i++ {
		xref += fmt.Sprintf("%010d 00000 n \n", offsets[i])
	}
	trailer := fmt.Sprintf("trailer\n<</Size 6 /Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", xrefStart)

	return []byte(header + obj1 + obj2 + obj3 + obj4 + obj5 + xref + trailer)
}
