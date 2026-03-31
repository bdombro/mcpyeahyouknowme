package main

import (
	"mcpyeahyouknowme/core"
)

// loadConfig delegates to core.LoadConfig.
func loadConfig(dataDir string) core.Config {
	return core.LoadConfig(dataDir)
}

// saveConfig delegates to core.SaveConfig.
func saveConfig(dataDir string, cfg core.Config) error {
	return core.SaveConfig(dataDir, cfg)
}

// configPath delegates to core.ConfigPath.
func configPath(dataDir string) string {
	return core.ConfigPath(dataDir)
}
