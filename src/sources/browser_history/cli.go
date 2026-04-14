// Package browser_history implements a local Chrome/Brave history source and MCP tools.
package browser_history

import (
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

// RunEnable enables browser_history. If a browser argument is provided (chrome or brave) it also stores that
// browser choice. Without an argument, the previously configured browser is kept and the source is simply enabled.
func RunEnable(dataDir string, args []string) {
	if len(args) > 0 {
		browser := normalizeBrowser(args[0])
		if browser == "" {
			// nocov
			slog.Error("unsupported browser", "browser", args[0], "supported", "chrome, brave")
			os.Exit(1)
		}
		if err := saveBrowserHistoryConfig(dataDir, BrowserHistoryConfig{Browser: browser}); err != nil {
			// nocov
			slog.Error("could not save browser_history config", "err", err)
			os.Exit(1)
		}
	} else {
		if loadBrowserHistoryConfig(dataDir).Browser == "" {
			fmt.Fprintln(os.Stderr, "Error: no browser configured; run: browser_history enable <chrome|brave>")
			os.Exit(1)
		}
	}
	if err := updateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Enabled = true
		sc.Reset = false
	}); err != nil {
		// nocov
		slog.Error("could not enable browser_history", "err", err)
		os.Exit(1)
	}

	browser := loadBrowserHistoryConfig(dataDir).Browser
	fmt.Printf("browser_history: enabled (%s)\n", browser)
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

// RunDisable sets browser_history disabled in config without wiping data or browser selection.
func RunDisable(dataDir string) {
	if err := updateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Enabled = false
	}); err != nil {
		// nocov
		slog.Error("could not disable browser_history", "err", err)
		os.Exit(1)
	}
	fmt.Println("browser_history: disabled")
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
		slog.Warn("warning during reset", "err", err)
	}

	if err := updateSourceConfig(dataDir, "browser_history", func(sc *core.SourceConfig) {
		sc.Enabled = false
		sc.Auth = nil
	}); err != nil {
		slog.Warn("could not update config.json", "err", err)
	}
	if err := core.ClearSearchSource(dataDir, "browser_history"); err != nil {
		slog.Warn("could not clear search index", "err", err)
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
