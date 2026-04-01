package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	dataDirOnce    sync.Once
	dataDirValue   string
	knownSourcesMu sync.RWMutex
	knownSources   []string
)

// DataDir returns the application data directory (~/.local/share/mcpyeahyouknowme).
func DataDir() string {
	dataDirOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dataDirValue = filepath.Join(home, ".local", "share", "mcpyeahyouknowme")
	})
	return dataDirValue
}

// IntArg extracts an integer from an MCP args map, returning def if absent.
func IntArg(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// BoolArg extracts a bool from an MCP args map, returning def if absent.
func BoolArg(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// JsonResult marshals v as indented JSON into a CallToolResult text response.
func JsonResult(v interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ToolDescription appends a compact example argument shape when provided.
func ToolDescription(summary, example string) string {
	if example == "" {
		return summary
	}
	return fmt.Sprintf("%s Example arguments: %s", summary, example)
}

// NewReadOnlyTool creates a read-only MCP tool with accurate annotations.
func NewReadOnlyTool(name, description string, opts ...mcp.ToolOption) mcp.Tool {
	toolOpts := []mcp.ToolOption{
		mcp.WithDescription(description),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	}
	return mcp.NewTool(name, append(toolOpts, opts...)...)
}

// NewMutatingTool creates a mutating MCP tool with accurate annotations.
func NewMutatingTool(name, description string, opts ...mcp.ToolOption) mcp.Tool {
	toolOpts := []mcp.ToolOption{
		mcp.WithDescription(description),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
	}
	return mcp.NewTool(name, append(toolOpts, opts...)...)
}

func missingArgumentResult(key, example string) *mcp.CallToolResult {
	msg := fmt.Sprintf("%s parameter is required", key)
	if example != "" {
		msg = fmt.Sprintf("%s; call with arguments: %s", msg, example)
	}
	return mcp.NewToolResultError(msg)
}

// RequireStringArgument returns an actionable MCP error when a required string
// argument is missing or not a string.
func RequireStringArgument(req mcp.CallToolRequest, key, example string) (string, *mcp.CallToolResult) {
	value, err := req.RequireString(key)
	if err == nil {
		return value, nil
	}
	return "", missingArgumentResult(key, example)
}

// RequireNumberArgument returns an actionable MCP error when a required number
// argument is missing or not numeric.
func RequireNumberArgument(req mcp.CallToolRequest, key, example string) (float64, *mcp.CallToolResult) {
	value, ok := req.GetArguments()[key]
	if !ok {
		return 0, missingArgumentResult(key, example)
	}
	switch n := value.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	default:
		return 0, missingArgumentResult(key, example)
	}
}

// RequireIntArgument returns an actionable MCP error when a required integer
// argument is missing or not numeric.
func RequireIntArgument(req mcp.CallToolRequest, key, example string) (int, *mcp.CallToolResult) {
	value, errResult := RequireNumberArgument(req, key, example)
	if errResult != nil {
		return 0, errResult
	}
	return int(value), nil
}

// RequireBoolArgument returns an actionable MCP error when a required boolean
// argument is missing or not a bool.
func RequireBoolArgument(req mcp.CallToolRequest, key, example string) (bool, *mcp.CallToolResult) {
	value, ok := req.GetArguments()[key]
	if !ok {
		return false, missingArgumentResult(key, example)
	}
	b, ok := value.(bool)
	if !ok {
		return false, missingArgumentResult(key, example)
	}
	return b, nil
}

// RegisterKnownSource records a source name so config normalization can keep a
// stable entry for it even when disabled.
func RegisterKnownSource(name string) {
	knownSourcesMu.Lock()
	defer knownSourcesMu.Unlock()
	if slices.Contains(knownSources, name) {
		return
	}
	knownSources = append(knownSources, name)
}

// KnownSources returns the currently registered source names.
func KnownSources() []string {
	knownSourcesMu.RLock()
	defer knownSourcesMu.RUnlock()
	return append([]string(nil), knownSources...)
}

// NormalizeConfig ensures config.json contains a stable entry for every known source.
func NormalizeConfig(cfg Config) Config {
	if cfg.Sources == nil {
		cfg.Sources = map[string]SourceConfig{}
	}
	for _, name := range KnownSources() {
		if _, ok := cfg.Sources[name]; !ok {
			cfg.Sources[name] = SourceConfig{}
		}
	}
	return cfg
}

// LoadConfig reads config.json from dataDir; returns an empty Config on any error.
func LoadConfig(dataDir string) Config {
	path := ConfigPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return NormalizeConfig(Config{Sources: map[string]SourceConfig{}})
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not parse config.json: %v\n", err)
		return NormalizeConfig(Config{Sources: map[string]SourceConfig{}})
	}
	return NormalizeConfig(cfg)
}

// SaveConfig writes cfg to {dataDir}/config.json atomically via a temp file.
func SaveConfig(dataDir string, cfg Config) error {
	cfg = NormalizeConfig(cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := ConfigPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ConfigPath returns the path to config.json within dataDir.
func ConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "config.json")
}

// UpdateSourceConfig loads, updates, and persists a single source config entry.
func UpdateSourceConfig(dataDir, name string, update func(*SourceConfig)) error {
	cfg := LoadConfig(dataDir)
	sc := cfg.Sources[name]
	update(&sc)
	cfg.Sources[name] = sc
	return SaveConfig(dataDir, cfg)
}

// SetSourceEnabled persists the top-level enabled state for a source.
func SetSourceEnabled(dataDir, name string, enabled bool) error {
	return UpdateSourceConfig(dataDir, name, func(sc *SourceConfig) {
		sc.Enabled = enabled
		sc.Reset = false
	})
}

// SetSourceDisabled persists a disabled source state after reset/auth loss.
func SetSourceDisabled(dataDir, name string) error {
	return SetSourceEnabled(dataDir, name, false)
}
