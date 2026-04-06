// Package notebook implements the notebook data source and MCP tools for local files.
package notebook

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"mcpyeahyouknowme/core"
)

const daemonLabel = "com.mcpyeahyouknowme.core"

var daemonUserHomeDir = os.UserHomeDir
var daemonStatPath = os.Stat
var daemonLaunchctlList = func(label string) ([]byte, error) {
	return exec.Command("launchctl", "list", label).Output()
}
var daemonSignalProcess = func(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}

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
		slog.Error("could not resolve path", "path", raw, "err", err)
		os.Exit(1)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		slog.Error("path is not an accessible directory", "path", abs)
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
		slog.Warn("could not save config", "err", err)
	}
	fmt.Printf("Added notebook directory: %s\n", abs)
	signalDaemonReindex()
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
		slog.Error("directory not configured", "path", abs)
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
		slog.Warn("could not save config", "err", err)
	}
	fmt.Printf("Removed notebook directory: %s\n", abs)
	signalDaemonReindex()
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
		slog.Warn("warning during reset", "err", err)
	}
	src.Close()

	if err := core.UpdateSourceConfig(dataDir, "notebook", func(sc *core.SourceConfig) {
		sc.Enabled = false
		sc.Auth = nil
	}); err != nil {
		slog.Warn("could not update config.json", "err", err)
	}
	if err := core.ClearSearchSource(dataDir, "notebook"); err != nil {
		slog.Warn("could not clear search index", "err", err)
	}
	fmt.Println("Notebook configuration reset.")
}

// signalDaemonReindex sends SIGUSR1 to the running daemon so it picks up config changes immediately instead of waiting for the next poll cycle.
func signalDaemonReindex() {
	pid := daemonPID()
	switch {
	case pid <= 0:
		fmt.Println("Start the daemon to begin indexing.")
	case daemonSignalProcess(pid, syscall.SIGUSR1) == nil:
		fmt.Println("Indexing will begin shortly.")
	default:
		fmt.Println("The daemon is running and will pick up notebook changes on its next refresh cycle.")
	}
}

// daemonPID returns the LaunchAgent PID when the core daemon is installed and running, or zero otherwise.
func daemonPID() int {
	if _, err := daemonStatPath(daemonPlistPath()); err != nil {
		return 0
	}
	out, err := daemonLaunchctlList(daemonLabel)
	if err != nil || len(out) == 0 {
		return 0
	}
	return parseLaunchctlPID(string(out))
}

// daemonPlistPath builds the LaunchAgent plist path used by the installed core daemon.
func daemonPlistPath() string {
	home, _ := daemonUserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", daemonLabel+".plist")
}

// parseLaunchctlPID extracts the numeric PID from `launchctl list` output for the core daemon label.
func parseLaunchctlPID(output string) int {
	re := regexp.MustCompile(`"PID"\s*=\s*(\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0
	}
	pid, _ := strconv.Atoi(matches[1])
	return pid
}

// marshalConfig encodes a NotebookConfig to JSON for storage in config.json.
func marshalConfig(cfg NotebookConfig) ([]byte, error) {
	return json.Marshal(cfg)
}
