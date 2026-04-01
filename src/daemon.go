package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"mcpyeahyouknowme/core"
)

const plistName = "com.mcpyeahyouknowme.core"

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistName+".plist")
}

func requireDaemonInstalled() string {
	plist := plistPath()
	if _, err := os.Stat(plist); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error: core daemon not installed. From the repo, run: ./scripts/install.sh")
		os.Exit(1)
	}
	return plist
}

func runStart() {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Started core daemon")
}

func runStop() {
	plist := requireDaemonInstalled()
	if err := exec.Command("launchctl", "unload", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Stopped core daemon")
}

func runRestart() {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error restarting daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Restarted core daemon")
}

func removeDaemon() {
	plist := plistPath()
	exec.Command("launchctl", "unload", plist).Run()
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error removing plist: %v\n", err)
	} else if err == nil {
		fmt.Println("Removed core daemon")
	}
}

func runReset() {
	removeDaemon()

	dDir := core.DataDir()
	if _, err := os.Stat(dDir); os.IsNotExist(err) {
		fmt.Println("Nothing to reset (data directory does not exist)")
		return
	}
	if err := os.RemoveAll(dDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing data directory %s: %v\n", dDir, err)
		os.Exit(1)
	}
	fmt.Printf("Removed all data: %s\n", dDir)
}

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

var commands = []string{
	"mcp",
	"info",
	"completions",
	"core",
	"start",
	"stop",
	"restart",
	"uninstall",
	"whatsapp",
	"gsuite",
	// Legacy (backward compatibility)
	"login",
	"reset",
}

func runCompletions(shell string) {
	switch shell {
	case "bash":
		printBashCompletions()
	case "zsh":
		printZshCompletions()
	default:
		fmt.Fprintf(os.Stderr, "Unsupported shell: %s (supported: bash, zsh)\n", shell)
		os.Exit(1)
	}
}

func printBashCompletions() {
	fmt.Print(`_mcpyeahyouknowme() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local cmd="${COMP_WORDS[1]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "mcp info completions core start stop restart uninstall whatsapp gsuite login reset" -- "$cur") )
        return
    fi

    if [[ $COMP_CWORD -eq 2 ]]; then
        case "$cmd" in
            whatsapp)
                COMPREPLY=( $(compgen -W "login reset" -- "$cur") )
                ;;
            gsuite)
                COMPREPLY=( $(compgen -W "login apps reset" -- "$cur") )
                ;;
            completions)
                COMPREPLY=( $(compgen -W "bash zsh" -- "$cur") )
                ;;
        esac
    fi
}
complete -o nospace -F _mcpyeahyouknowme mcpyeahyouknowme
`)
}

func printZshCompletions() {
	fmt.Print(`_mcpyeahyouknowme() {
    local -a cmds wa_cmds gs_cmds comp_args

    cmds=(
        'mcp:Start the MCP server (stdio transport)'
        'info:Show install status and data locations'
        'completions:Print shell completions (bash or zsh)'
        'core:Run the daemon process directly (used by LaunchAgent)'
        'start:Start the core daemon'
        'stop:Stop the core daemon'
        'restart:Restart the core daemon'
        'uninstall:Remove daemon, data, and binaries'
        'whatsapp:WhatsApp commands'
        'gsuite:Google Suite commands'
    )
    wa_cmds=(
        'login:Log in to WhatsApp (scan QR code)'
        'reset:Wipe WhatsApp data and session'
    )
    gs_cmds=(
        'login:Authenticate with Google (all apps)'
        'apps:View/toggle enabled Google apps'
        'reset:Clear all Google Suite data and token'
    )
    comp_args=(
        'bash:Bash completions'
        'zsh:Zsh completions'
    )

    if (( CURRENT == 2 )); then
        _describe -t commands 'command' cmds
    elif (( CURRENT == 3 )) && [[ "${words[2]}" == whatsapp ]]; then
        _describe -t wa_commands 'whatsapp command' wa_cmds
    elif (( CURRENT == 3 )) && [[ "${words[2]}" == gsuite ]]; then
        _describe -t gs_commands 'gsuite command' gs_cmds
    else
        case "${words[2]}" in
            completions)
                _describe -t shells 'shell' comp_args
                ;;
        esac
    fi
}

if (( ! $+functions[compdef] )); then
    autoload -Uz compinit && compinit -C
fi
compdef _mcpyeahyouknowme mcpyeahyouknowme
`)
}
