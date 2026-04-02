package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mcpyeahyouknowme/core"
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
		fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Started core daemon")
}

// runStop unloads the LaunchAgent plist so macOS stops the core daemon.
func runStop() {
	plist := requireDaemonInstalled()
	if err := exec.Command("launchctl", "unload", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Stopped core daemon")
}

// runRestart reloads the LaunchAgent plist so macOS restarts the core daemon process.
func runRestart() {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error restarting daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Restarted core daemon")
}

// removeDaemon unloads and deletes the LaunchAgent plist so the daemon no longer auto-starts.
func removeDaemon() {
	plist := plistPath()
	exec.Command("launchctl", "unload", plist).Run()
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error removing plist: %v\n", err)
	} else if err == nil {
		fmt.Println("Removed core daemon")
	}
}

// runReset removes the daemon and all app data so the install returns to a clean state.
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

// runCompletions prints shell completion code for shell, or exits if the shell is unsupported.
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

// printBashCompletions writes the bash completion function so users can source it in their shell.
func printBashCompletions() {
	topLevel := strings.Join(commandNames(topLevelCommands()), " ")
	whatsAppSubs := strings.Join(commandNames(findCommand(topLevelCommands(), "whatsapp").Subcommands), " ")
	gsuiteSubs := strings.Join(commandNames(findCommand(topLevelCommands(), "gsuite").Subcommands), " ")

	fmt.Printf(`_mcpyeahyouknowme() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local cmd="${COMP_WORDS[1]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "%s" -- "$cur") )
        return
    fi

    if [[ $COMP_CWORD -eq 2 ]]; then
        case "$cmd" in
            whatsapp)
                COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                ;;
            gsuite)
                COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                ;;
            completions)
                COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                ;;
        esac
    fi
}
complete -o nospace -F _mcpyeahyouknowme mcpyeahyouknowme
`, topLevel, whatsAppSubs, gsuiteSubs, shellCompletionWords())
}

// printZshCompletions writes the zsh completion function so users can source it in their shell.
func printZshCompletions() {
	fmt.Printf(`_mcpyeahyouknowme() {
    local -a cmds wa_cmds gs_cmds comp_args

    cmds=(
%s
    )
    wa_cmds=(
%s
    )
    gs_cmds=(
%s
    )
    comp_args=(
%s
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
`, zshEntries(topLevelCommands()),
		zshEntries(findCommand(topLevelCommands(), "whatsapp").Subcommands),
		zshEntries(findCommand(topLevelCommands(), "gsuite").Subcommands),
		zshChoiceEntries(findCommand(topLevelCommands(), "completions").ArgChoices))
}

// zshEntries formats command metadata as zsh `_describe` entries for completion menus.
func zshEntries(commands []Command) string {
	lines := make([]string, 0, len(commands))
	for _, cmd := range commands {
		lines = append(lines, fmt.Sprintf("        '%s:%s'", cmd.Name, cmd.Summary))
	}
	return strings.Join(lines, "\n")
}

// zshChoiceEntries formats argument choices as zsh `_describe` entries for completion menus.
func zshChoiceEntries(choices []Choice) string {
	lines := make([]string, 0, len(choices))
	for _, choice := range choices {
		lines = append(lines, fmt.Sprintf("        '%s:%s'", choice.Value, choice.Summary))
	}
	return strings.Join(lines, "\n")
}
