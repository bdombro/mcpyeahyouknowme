package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	dataDirOnce    sync.Once
	dataDirValue   string
	knownSourcesMu sync.RWMutex
	knownSources   []string
)

// DataDir returns the shared app data root so CLI, daemon, MCP, models, and SQLite files resolve to one stable location.
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

// IntArg reads key from MCP-style args, coercing numeric JSON values to int and falling back to def when absent or non-numeric.
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

// BoolArg reads key from MCP-style args, returning def when the caller omitted it or passed a non-bool.
func BoolArg(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// JsonResult serializes v into the text payload shape MCP handlers return, converting marshal failures into tool errors.
func JsonResult(v interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// ToolDescription builds short MCP tool help text, appending one compact argument example so clients can retry correctly.
func ToolDescription(summary, example string) string {
	if example == "" {
		return summary
	}
	return fmt.Sprintf("%s Example arguments: %s", summary, example)
}

// NewReadOnlyTool wraps mcp.NewTool with read-only annotations so clients can reason about safe calls from schema alone.
func NewReadOnlyTool(name, description string, opts ...mcp.ToolOption) mcp.Tool {
	toolOpts := []mcp.ToolOption{
		mcp.WithDescription(description),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	}
	return mcp.NewTool(name, append(toolOpts, opts...)...)
}

// NewMutatingTool wraps mcp.NewTool with mutating annotations so clients treat the tool as state-changing and non-idempotent.
func NewMutatingTool(name, description string, opts ...mcp.ToolOption) mcp.Tool {
	toolOpts := []mcp.ToolOption{
		mcp.WithDescription(description),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
	}
	return mcp.NewTool(name, append(toolOpts, opts...)...)
}

// missingArgumentResult builds a retry-oriented MCP error for a missing required arg, optionally showing the caller a valid payload shape.
func missingArgumentResult(key, example string) *mcp.CallToolResult {
	msg := fmt.Sprintf("%s parameter is required", key)
	if example != "" {
		msg = fmt.Sprintf("%s; call with arguments: %s", msg, example)
	}
	return mcp.NewToolResultError(msg)
}

// RequireStringArgument is the standard required-string gate for MCP handlers, returning a retryable tool error instead of a raw parse failure.
func RequireStringArgument(req mcp.CallToolRequest, key, example string) (string, *mcp.CallToolResult) {
	value, err := req.RequireString(key)
	if err == nil {
		return value, nil
	}
	return "", missingArgumentResult(key, example)
}

// RequireNumberArgument is the standard required-number gate for MCP handlers, accepting common numeric types and otherwise returning a retryable tool error.
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

// RequireIntArgument layers integer coercion on RequireNumberArgument so MCP handlers get an int plus the same retryable error contract.
func RequireIntArgument(req mcp.CallToolRequest, key, example string) (int, *mcp.CallToolResult) {
	value, errResult := RequireNumberArgument(req, key, example)
	if errResult != nil {
		return 0, errResult
	}
	return int(value), nil
}

// RequireBoolArgument is the standard required-bool gate for MCP handlers, returning a retryable tool error when the arg is missing or not boolean.
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

// IsLowValueContent applies the semantic-search chunk filter, rejecting long text that is too numeric/punctuation-heavy to justify embedding.
func IsLowValueContent(text string) bool {
	nonWhitespace := 0
	letters := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		nonWhitespace++
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if nonWhitespace < 50 {
		return false
	}
	return float64(letters)/float64(nonWhitespace) < 0.30
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

// KnownSources returns a copy of the registered source names so config code can iterate without mutating shared state.
func KnownSources() []string {
	knownSourcesMu.RLock()
	defer knownSourcesMu.RUnlock()
	return append([]string(nil), knownSources...)
}

// NormalizeConfig adds missing source slots into cfg and returns it so LoadConfig/SaveConfig preserve every known source in config.json.
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

// LoadConfig reads config.json from dataDir, warning on parse failure and returning a normalized empty config on any read/parse error.
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

// ConfigPath resolves the config.json path inside dataDir so all config readers and writers hit the same file.
func ConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "config.json")
}

// UpdateSourceConfig is the single-source config patch helper, loading normalized config, mutating one source via `update`, then persisting it.
func UpdateSourceConfig(dataDir, name string, update func(*SourceConfig)) error {
	cfg := LoadConfig(dataDir)
	sc := cfg.Sources[name]
	update(&sc)
	cfg.Sources[name] = sc
	return SaveConfig(dataDir, cfg)
}

// SetSourceEnabled persists a source's enabled bit and clears any pending reset flag so the daemon can start it normally.
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
