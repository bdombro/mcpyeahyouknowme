package notebook

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerTools registers all notebook read-only tools under the notebook_ prefix.
func registerTools(src *Source, s *server.MCPServer) {
	prefix := src.Name() + "_"
	cfg := loadNotebookConfig(src.dataDir)

	s.AddTool(core.NewReadOnlyTool(prefix+"list",
		core.ToolDescription("List files in configured notebook directories.", `{"type":"md","query":"meeting"}`),
		mcp.WithString("type", mcp.Description("Filter by file type: md, pdf, or image")),
		mcp.WithString("query", mcp.Description("Substring filter on file title or path")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleList(cfg, req)
	})

	s.AddTool(core.NewReadOnlyTool(prefix+"read",
		core.ToolDescription("Read a markdown or text file from a notebook directory.", `{"path":"notes/project.md"}`),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the file")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleRead(cfg, req)
	})

	s.AddTool(core.NewReadOnlyTool(prefix+"read_pdf",
		core.ToolDescription("Extract and return text content from a PDF file in a notebook directory.", `{"path":"docs/report.pdf"}`),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the PDF")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleReadPDF(cfg, req)
	})

	s.AddTool(core.NewReadOnlyTool(prefix+"get_image",
		core.ToolDescription("Return a base64-encoded image from a notebook directory for AI interpretation.", `{"path":"images/diagram.png"}`),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the image")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGetImage(cfg, req)
	})
}

// FileInfo describes one file for the list tool response.
type FileInfo struct {
	Path     string `json:"path"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	ModTime  string `json:"mod_time"`
	Dir      string `json:"dir"`
}

// handleList walks configured dirs and returns matching file summaries.
func handleList(cfg NotebookConfig, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	typeFilter := ""
	if t, ok := req.GetArguments()["type"]; ok {
		if ts, ok := t.(string); ok {
			typeFilter = strings.ToLower(strings.TrimSpace(ts))
		}
	}
	queryFilter := ""
	if q, ok := req.GetArguments()["query"]; ok {
		if qs, ok := q.(string); ok {
			queryFilter = strings.ToLower(qs)
		}
	}

	var results []FileInfo
	for _, dir := range cfg.Dirs {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != dir {
					return filepath.SkipDir
				}
				return nil
			}
			ft := fileTypeOf(d.Name())
			if ft == "" {
				return nil
			}
			if typeFilter != "" && ft != typeFilter {
				return nil
			}
			info, _ := d.Info()
			title := stemName(d.Name())
			rel, _ := filepath.Rel(dir, path)
			if queryFilter != "" {
				if !strings.Contains(strings.ToLower(rel), queryFilter) &&
					!strings.Contains(strings.ToLower(title), queryFilter) {
					return nil
				}
			}
			modTime := ""
			if info != nil {
				modTime = info.ModTime().Format(time.RFC3339)
			}
			results = append(results, FileInfo{
				Path:    path,
				Title:   title,
				Type:    ft,
				ModTime: modTime,
				Dir:     dir,
			})
			return nil
		})
	}
	return core.JsonResult(results)
}

// handleRead reads a markdown or text file and returns its contents.
func handleRead(cfg NotebookConfig, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, errResult := core.RequireStringArgument(req, "path", `{"path":"notes/project.md"}`)
	if errResult != nil {
		return errResult, nil
	}
	path = resolvePath(cfg, path)
	if !isPathInConfiguredDirs(path, cfg.Dirs) {
		return mcp.NewToolResultError(fmt.Sprintf("path %q is not inside any configured notebook directory", path)), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// handleReadPDF extracts text from a PDF and returns it, using Vision OCR as a fallback for scanned documents.
func handleReadPDF(cfg NotebookConfig, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, errResult := core.RequireStringArgument(req, "path", `{"path":"docs/report.pdf"}`)
	if errResult != nil {
		return errResult, nil
	}
	path = resolvePath(cfg, path)
	if !isPathInConfiguredDirs(path, cfg.Dirs) {
		return mcp.NewToolResultError(fmt.Sprintf("path %q is not inside any configured notebook directory", path)), nil
	}
	_, content, err := extractPDF(path, VisionAnalyzer{}) // nocov — calls real CGO Vision for scanned PDFs
	if err != nil {                                       // nocov
		return mcp.NewToolResultError(err.Error()), nil // nocov
	} // nocov
	if content == "" { // nocov
		return mcp.NewToolResultText("(no extractable text found)"), nil // nocov
	} // nocov
	return mcp.NewToolResultText(content), nil // nocov
}

// handleGetImage returns a base64-encoded image for the AI client to interpret directly.
func handleGetImage(cfg NotebookConfig, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, errResult := core.RequireStringArgument(req, "path", `{"path":"images/diagram.png"}`)
	if errResult != nil {
		return errResult, nil
	}
	path = resolvePath(cfg, path)
	if !isPathInConfiguredDirs(path, cfg.Dirs) {
		return mcp.NewToolResultError(fmt.Sprintf("path %q is not inside any configured notebook directory", path)), nil
	}
	b64, err := readFileBase64(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result := map[string]string{
		"path":   path,
		"base64": b64,
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// resolvePath tries to resolve a relative path against each configured directory, returning the first match found.
func resolvePath(cfg NotebookConfig, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	for _, dir := range cfg.Dirs {
		candidate := filepath.Join(dir, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// Return as-is; the caller will validate.
	return path
}
