// Package main is the mcpyeahyouknowme CLI and MCP server entrypoint.
package main

import (
	"os"
	"path/filepath"

	"mcpyeahyouknowme/core"
)

// Build-time variables set via -ldflags
var (
	BuildTime    = "unknown"
	BuildVersion = "dev"
)

// main sets the process-wide tokenizer cache path before any command runs, then hands process args to CLI dispatch.
func main() {
	os.Setenv("GO_TOKENIZER", filepath.Join(core.DataDir(), "cache", "tokenizer"))
	dispatchCLI(os.Args[1:])
}
