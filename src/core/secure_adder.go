package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type secureToolAdder struct {
	inner    *server.MCPServer
	cfg      McpConfig
	disabled map[string]struct{}
	audit    *AuditLogger
	limiter  *RateLimiter
}

// NewSecureToolAdder wraps srv so AddTool applies disabled_tools, read_only, audit logging, and per-tool mutating rate limits.
func NewSecureToolAdder(srv *server.MCPServer, cfg McpConfig, dataDir string) ToolAdder {
	audit, err := NewAuditLogger(dataDir)
	if err != nil {
		slog.Warn("mcp audit log unavailable; tool calls will not be recorded", "data_dir", dataDir, "err", err)
	}
	disabled := make(map[string]struct{})
	for _, name := range cfg.DisabledTools {
		disabled[name] = struct{}{}
	}
	lim := NewRateLimiter(cfg.EffectiveMutatingToolsPerMin(), time.Minute)
	return &secureToolAdder{
		inner:    srv,
		cfg:      cfg,
		disabled: disabled,
		audit:    audit,
		limiter:  lim,
	}
}

// AddTool registers the tool on the inner server after applying security gates and wrapping the handler.
func (a *secureToolAdder) AddTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	if a == nil || a.inner == nil || handler == nil {
		return
	}
	if _, skip := a.disabled[tool.Name]; skip {
		return
	}
	if a.cfg.ReadOnly && !toolIsReadOnly(tool) {
		return
	}
	name := tool.Name
	wrapped := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a.audit.Log(name, req.GetArguments())
		if toolNeedsRateLimit(tool) && !a.limiter.Allow(name) {
			return mcp.NewToolResultError(fmt.Sprintf("rate limit: too many mutating tool calls for %s this minute", name)), nil
		}
		return handler(ctx, req)
	}
	a.inner.AddTool(tool, wrapped)
}

func toolIsReadOnly(tool mcp.Tool) bool {
	h := tool.Annotations.ReadOnlyHint
	return h != nil && *h
}

func toolNeedsRateLimit(tool mcp.Tool) bool {
	return !toolIsReadOnly(tool)
}
