package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	fmt.Printf(`_mcpyeahyouknowme() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local cmd="${COMP_WORDS[1]}"
    local subcmd="${COMP_WORDS[2]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "%s" -- "$cur") )
        return
    fi

    if [[ $COMP_CWORD -eq 2 ]]; then
        case "$cmd" in
%s
        esac
    elif [[ $COMP_CWORD -eq 3 ]]; then
        case "$cmd:$subcmd" in
%s
        esac
    fi
}
complete -o nospace -F _mcpyeahyouknowme mcpyeahyouknowme
`, topLevel, bashSecondWordCases(topLevelCommands()), bashThirdWordCases(topLevelCommands()))
}

// printZshCompletions writes the zsh completion function so users can source it in their shell.
func printZshCompletions() {
	fmt.Printf(`_mcpyeahyouknowme() {
    local -a cmds values

    cmds=(
%s
    )

    if (( CURRENT == 2 )); then
        _describe -t commands 'command' cmds
    elif (( CURRENT == 3 )); then
        case "${words[2]}" in
%s
        esac
    elif (( CURRENT == 4 )); then
        case "${words[2]}:${words[3]}" in
%s
        esac
    fi
}

if (( ! $+functions[compdef] )); then
    autoload -Uz compinit && compinit -C
fi
compdef _mcpyeahyouknowme mcpyeahyouknowme
`, zshEntries(topLevelCommands(), "        "),
		zshSecondWordCases(topLevelCommands()),
		zshThirdWordCases(topLevelCommands()))
}

// bashSecondWordCases renders bash completion branches for top-level subcommands and constrained arguments.
func bashSecondWordCases(commands []Command) string {
	lines := make([]string, 0, len(commands))
	for _, cmd := range commands {
		switch {
		case len(cmd.Subcommands) > 0:
			lines = append(lines, fmt.Sprintf(`            %s)
                COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                ;;`, cmd.Name, strings.Join(commandNames(cmd.Subcommands), " ")))
		case len(cmd.ArgChoices) > 0:
			lines = append(lines, fmt.Sprintf(`            %s)
                COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                ;;`, cmd.Name, strings.Join(choiceValues(cmd.ArgChoices), " ")))
		}
	}
	return strings.Join(lines, "\n")
}

// bashThirdWordCases renders bash completion branches for subcommand arguments with explicit choice sets.
func bashThirdWordCases(commands []Command) string {
	lines := []string{}
	for _, cmd := range commands {
		for _, subcmd := range cmd.Subcommands {
			if len(subcmd.ArgChoices) == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf(`            %s:%s)
                COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                ;;`, cmd.Name, subcmd.Name, strings.Join(choiceValues(subcmd.ArgChoices), " ")))
		}
	}
	return strings.Join(lines, "\n")
}

// zshSecondWordCases renders zsh completion branches for top-level subcommands and constrained arguments.
func zshSecondWordCases(commands []Command) string {
	lines := make([]string, 0, len(commands))
	for _, cmd := range commands {
		switch {
		case len(cmd.Subcommands) > 0:
			lines = append(lines, fmt.Sprintf(`            %s)
                values=(
%s
                )
                _describe -t subcommands 'subcommand' values
                ;;`, cmd.Name, zshEntries(cmd.Subcommands, "                    ")))
		case len(cmd.ArgChoices) > 0:
			lines = append(lines, fmt.Sprintf(`            %s)
                values=(
%s
                )
                _describe -t arguments 'argument' values
                ;;`, cmd.Name, zshChoiceEntries(cmd.ArgChoices, "                    ")))
		}
	}
	return strings.Join(lines, "\n")
}

// zshThirdWordCases renders zsh completion branches for subcommand arguments with explicit choice sets.
func zshThirdWordCases(commands []Command) string {
	lines := []string{}
	for _, cmd := range commands {
		for _, subcmd := range cmd.Subcommands {
			if len(subcmd.ArgChoices) == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf(`            %s:%s)
                values=(
%s
                )
                _describe -t arguments 'argument' values
                ;;`, cmd.Name, subcmd.Name, zshChoiceEntries(subcmd.ArgChoices, "                    ")))
		}
	}
	return strings.Join(lines, "\n")
}

// zshEntries formats command metadata as zsh `_describe` entries so generated branches can reuse one renderer.
func zshEntries(commands []Command, indent string) string {
	lines := make([]string, 0, len(commands))
	for _, cmd := range commands {
		lines = append(lines, fmt.Sprintf("%s'%s:%s'", indent, cmd.Name, cmd.Summary))
	}
	return strings.Join(lines, "\n")
}

// zshChoiceEntries formats argument choices as zsh `_describe` entries so explicit CLI choices stay discoverable.
func zshChoiceEntries(choices []Choice, indent string) string {
	lines := make([]string, 0, len(choices))
	for _, choice := range choices {
		lines = append(lines, fmt.Sprintf("%s'%s:%s'", indent, choice.Value, choice.Summary))
	}
	return strings.Join(lines, "\n")
}
