package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

const plistName = "com.mcpyeahyouknowme.core"

// plistPath returns the LaunchAgent plist path so daemon commands target the installed service file.
func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistName+".plist")
}

// requireDaemonInstalled returns the installed plist path, or exits after printing install guidance.
func requireDaemonInstalled() string {
	plist := plistPath()
	if _, err := os.Stat(plist); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error: core daemon not installed. From the repo, run: ./scripts/install.sh")
		os.Exit(1)
	}
	return plist
}

// runStart reloads the LaunchAgent plist so macOS starts the core daemon.
func runStart() {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		slog.Error("error starting daemon", "err", err)
		os.Exit(1)
	}
	fmt.Println("Started core daemon")
}

// runStop unloads the LaunchAgent plist so macOS stops the core daemon.
func runStop() {
	plist := requireDaemonInstalled()
	if err := exec.Command("launchctl", "unload", plist).Run(); err != nil {
		slog.Error("error stopping daemon", "err", err)
		os.Exit(1)
	}
	fmt.Println("Stopped core daemon")
}

// runRestart reloads the LaunchAgent plist so macOS restarts the core daemon process.
func runRestart() {
	if err := restartInstalledDaemon(); err != nil {
		slog.Error("error restarting daemon", "err", err)
		os.Exit(1)
	}
	fmt.Println("Restarted core daemon")
}

// Restarts the installed LaunchAgent so CLI reset paths can reopen daemon-owned SQLite handles after on-disk files are cleared.
func restartInstalledDaemon() error {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		return err
	}
	return nil
}

// runUninstall prints the supported uninstall path and the side effects the script handles.
func runUninstall() {
	fmt.Println("⚠️  For a complete uninstall, please use the uninstall script:")
	fmt.Println()
	fmt.Println("  cd /path/to/mcpyeahyouknowme")
	fmt.Println("  ./scripts/uninstall.sh")
	fmt.Println()
	fmt.Println("The uninstall script will:")
	fmt.Println("  • Kill all mcpyeahyouknowme processes")
	fmt.Println("  • Clean up database lock files")
	fmt.Println("  • Unload and remove the daemon")
	fmt.Println("  • Remove the data directory")
	fmt.Println("  • Remove shell completions")
	fmt.Println("  • Remove the binary")
	fmt.Println()
	fmt.Println("If you don't have access to the repository, you can manually:")
	fmt.Println("  1. pkill -9 -f mcpyeahyouknowme")
	fmt.Println("  2. launchctl unload ~/Library/LaunchAgents/com.mcpyeahyouknowme.core.plist")
	fmt.Println("  3. rm ~/Library/LaunchAgents/com.mcpyeahyouknowme.core.plist")
	fmt.Println("  4. rm -rf ~/.local/share/mcpyeahyouknowme")
	fmt.Println("  5. Remove completions from ~/.zshrc")
	fmt.Println("  6. sudo rm /usr/local/bin/mcpyeahyouknowme")
}
