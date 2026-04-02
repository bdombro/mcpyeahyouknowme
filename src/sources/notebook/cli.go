package notebook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mcpyeahyouknowme/core"
)

// RunAdd adds a directory to the notebook source configuration, enabling the source on first add.
func RunAdd(dataDir string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: path argument required")
		fmt.Fprintln(os.Stderr, "Usage: mcpyeahyouknowme notebook add <path>")
		os.Exit(1)
	}

	raw := args[0]
	abs, err := filepath.Abs(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not resolve path %q: %v\n", raw, err)
		os.Exit(1)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %q is not an accessible directory\n", abs)
		os.Exit(1)
	}

	cfg := loadNotebookConfig(dataDir)
	for _, d := range cfg.Dirs {
		if d == abs {
			fmt.Printf("Directory already configured: %s\n", abs)
			return
		}
	}
	cfg.Dirs = append(cfg.Dirs, abs)

	data, _ := marshalConfig(cfg)
	if err := core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		sc.Enabled = true
		sc.Reset = false
		sc.Auth = data
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
	}
	fmt.Printf("Added notebook directory: %s\n", abs)
	fmt.Println("Run 'mcpyeahyouknowme core' or restart the daemon to index new files.")
}

// RunRemove removes a directory from the notebook source configuration, disabling the source when no dirs remain.
func RunRemove(dataDir string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: path argument required")
		fmt.Fprintln(os.Stderr, "Usage: mcpyeahyouknowme notebook remove <path>")
		os.Exit(1)
	}

	abs, _ := filepath.Abs(args[0])
	cfg := loadNotebookConfig(dataDir)

	var remaining []string
	found := false
	for _, d := range cfg.Dirs {
		if d == abs {
			found = true
		} else {
			remaining = append(remaining, d)
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Directory not configured: %s\n", abs)
		os.Exit(1)
	}
	cfg.Dirs = remaining

	// Prune cache entries for the removed directory.
	src := NewSource(dataDir)
	if src.db != nil {
		_ = PruneDir(src.db, abs)
		src.Close()
	}

	data, _ := marshalConfig(cfg)
	if err := core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		if len(remaining) == 0 {
			sc.Enabled = false
		}
		sc.Auth = data
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
	}
	fmt.Printf("Removed notebook directory: %s\n", abs)
}

// RunList prints all configured notebook directories with per-type file counts.
func RunList(dataDir string) {
	cfg := loadNotebookConfig(dataDir)
	if len(cfg.Dirs) == 0 {
		fmt.Println("No notebook directories configured.")
		fmt.Println("Run 'mcpyeahyouknowme notebook add <path>' to add one.")
		return
	}
	fmt.Println("Configured notebook directories:")
	fmt.Println()
	for i, dir := range cfg.Dirs {
		counts := countFilesInDir(dir)
		fmt.Printf("  %d. %s\n", i+1, dir)
		fmt.Printf("     %d markdown, %d PDF, %d image files\n",
			counts["md"], counts["pdf"], counts["image"])
	}
}

// RunReset clears all notebook configuration and deletes the file cache database.
func RunReset(dataDir string) {
	fmt.Print("Are you sure you want to remove all notebook configuration? (yes/no): ")
	var response string
	fmt.Scanln(&response)
	if strings.ToLower(response) != "yes" {
		fmt.Println("Cancelled.")
		return
	}

	src := NewSource(dataDir)
	if err := src.Reset(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning during reset: %v\n", err)
	}
	src.Close()

	if err := core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		sc.Enabled = false
		sc.Auth = nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update config.json: %v\n", err)
	}
	fmt.Println("Notebook configuration reset.")
}

// marshalConfig encodes a NotebookConfig to JSON for storage in config.json.
func marshalConfig(cfg NotebookConfig) ([]byte, error) {
	return json.Marshal(cfg)
}
