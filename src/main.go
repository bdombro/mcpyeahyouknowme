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

func main() {
	os.Setenv("GO_TOKENIZER", filepath.Join(core.DataDir(), "cache", "tokenizer"))
	dispatchCLI(os.Args[1:])
}
