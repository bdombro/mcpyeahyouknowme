package main

import (
	"mcpyeahyouknowme/core"
)

// loadConfig delegates to core.LoadConfig.
func loadConfig(dataDir string) core.Config {
	return core.LoadConfig(dataDir)
}
