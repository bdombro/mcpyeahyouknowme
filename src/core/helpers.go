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
