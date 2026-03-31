package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	dataDirOnce  sync.Once
	dataDirValue string
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

// LoadConfig reads config.json from dataDir; returns an empty Config on any error.
func LoadConfig(dataDir string) Config {
	path := ConfigPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{Sources: map[string]SourceConfig{}}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not parse config.json: %v\n", err)
		return Config{Sources: map[string]SourceConfig{}}
	}
	if cfg.Sources == nil {
		cfg.Sources = map[string]SourceConfig{}
	}
	return cfg
}

// SaveConfig writes cfg to {dataDir}/config.json atomically via a temp file.
func SaveConfig(dataDir string, cfg Config) error {
	if cfg.Sources == nil {
		cfg.Sources = map[string]SourceConfig{}
	}
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
