// Package browser_history implements a local Chrome/Brave history source and MCP tools.
package browser_history

import (
	"fmt"
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

type resetter interface {
	Reset(dataDir string) error
}

var newResetSource = func(dataDir string) resetter { return NewSource(dataDir) }
var daemonUserHomeDir = os.UserHomeDir
var daemonStatPath = os.Stat
var daemonLaunchctlList = func(label string) ([]byte, error) {
	return exec.Command("launchctl", "list", label).Output()
}
var daemonSignalProcess = func(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}

// RunEnable enables browser_history and stores the selected browser for daemon-owned indexing.
func RunEnable(dataDir string, args []string) {
	if len(args) == 0 {
		// nocov
		fmt.Fprintln(os.Stderr, "Error: browser argument required")
		fmt.Fprintln(os.Stderr, "Usage: mcpyeahyouknowme browser_history enable <chrome|brave>")
		os.Exit(1)
	}

	browser := normalizeBrowser(args[0])
	if browser == "" {
		// nocov
		fmt.Fprintf(os.Stderr, "Error: unsupported browser %q (expected chrome or brave)\n", args[0])
		os.Exit(1)
	}

	cfg := BrowserHistoryConfig{Browser: browser}
	if err := saveBrowserHistoryConfig(dataDir, cfg); err != nil {
		// nocov
		fmt.Fprintf(os.Stderr, "Error: could not save browser_history config: %v\n", err)
		os.Exit(1)
	}
	if err := updateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Enabled = true
		sc.Reset = false
	}); err != nil {
		// nocov
		fmt.Fprintf(os.Stderr, "Error: could not enable browser_history: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Enabled browser_history for %s.\n", browser)
	pid := daemonPID()
	switch {
	case pid <= 0:
		fmt.Println("Start the daemon to begin indexing browser history.")
	case daemonSignalProcess(pid, syscall.SIGUSR1) == nil:
		fmt.Println("Indexing will begin shortly.")
	default:
		fmt.Println("The daemon is running and will pick up browser_history on its next refresh cycle.")
	}
}

// RunReset clears browser_history config and deletes local snapshot files after explicit confirmation.
func RunReset(dataDir string) {
	fmt.Print("Are you sure you want to reset browser_history data? (yes/no): ")
	var response string
	fmt.Scanln(&response)
	if strings.ToLower(strings.TrimSpace(response)) != "yes" {
		fmt.Println("Cancelled.")
		return
	}

	src := newResetSource(dataDir)
	if err := src.Reset(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning during reset: %v\n", err)
	}

	if err := updateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Enabled = false
		sc.Auth = nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update config.json: %v\n", err)
	}
	if err := core.ClearSearchSource(dataDir, "browser_history"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not clear search index: %v\n", err)
	}
	fmt.Println("browser_history reset.")
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
