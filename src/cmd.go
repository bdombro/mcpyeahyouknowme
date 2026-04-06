package main

import (
	"fmt"
	"os"
	"strings"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/browser_history"
	"mcpyeahyouknowme/sources/gsuite"
	"mcpyeahyouknowme/sources/notebook"
	"mcpyeahyouknowme/sources/whatsapp"
)

type Choice struct {
	Value   string
	Summary string
}

type Command struct {
	Name        string
	Summary     string
	Usage       string
	Section     string
	Subcommands []Command
	ArgChoices  []Choice
	Run         func(args []string)
}

// commandTree builds the canonical CLI graph so dispatch, usage, and shell completions stay in sync from one definition.
func commandTree() []Command {
	dataDir := core.DataDir()
	return []Command{
		{
			Name:    "mcp",
			Summary: "Start the MCP server (stdio transport)",
			Usage:   "mcp",
			Section: "General",
			Run: func(_ []string) {
				runMcp()
			},
		},
		{
			Name:    "status",
			Summary: "Show install status and data locations",
			Usage:   "status [--json] [--live]",
			Section: "General",
			Run: func(args []string) {
				runStatus(args)
			},
		},
		{
			Name:    "completions",
			Summary: "Print shell completions (bash or zsh)",
			Usage:   "completions [shell]",
			Section: "General",
			ArgChoices: []Choice{
				{Value: "bash", Summary: "Bash completions"},
				{Value: "zsh", Summary: "Zsh completions"},
			},
			Run: func(args []string) {
				shell := "zsh"
				if len(args) > 0 {
					shell = args[0]
				}
				runCompletions(shell)
			},
		},
		{
			Name:    "core",
			Summary: "Run the daemon process directly (used by LaunchAgent)",
			Usage:   "core",
			Section: "Core Daemon",
			Run: func(_ []string) {
				runCore()
			},
		},
		{
			Name:    "start",
			Summary: "Start the core daemon",
			Usage:   "start",
			Section: "Core Daemon",
			Run: func(_ []string) {
				runStart()
			},
		},
		{
			Name:    "stop",
			Summary: "Stop the core daemon",
			Usage:   "stop",
			Section: "Core Daemon",
			Run: func(_ []string) {
				runStop()
			},
		},
		{
			Name:    "restart",
			Summary: "Restart the core daemon",
			Usage:   "restart",
			Section: "Core Daemon",
			Run: func(_ []string) {
				runRestart()
			},
		},
		{
			Name:    "reindex",
			Summary: "Rebuild the search index from scratch",
			Usage:   "reindex",
			Section: "Maintenance",
			Run: func(args []string) {
				runReindex(args)
			},
		},
		{
			Name:    "reset",
			Summary: "Reset all source connections and data",
			Usage:   "reset",
			Section: "Maintenance",
			Run: func(_ []string) {
				resetAllRunner(dataDir)
			},
		},
		{
			Name:    "uninstall",
			Summary: "Instructions for proper uninstall (use ./scripts/uninstall.sh)",
			Usage:   "uninstall",
			Section: "Maintenance",
			Run: func(_ []string) {
				runUninstall()
			},
		},
		{
			Name:    "whatsapp",
			Summary: "WhatsApp commands",
			Section: "WhatsApp",
			Subcommands: []Command{
				{
					Name:    "login",
					Summary: "Log in to WhatsApp (scan QR code)",
					Usage:   "whatsapp login [--relogin]",
					Run: func(args []string) {
						whatsapp.RunLogin(dataDir, args)
					},
				},
				{
					Name:    "reset",
					Summary: "Wipe WhatsApp data and session",
					Usage:   "whatsapp reset",
					Run: func(_ []string) {
						whatsapp.RunReset(dataDir)
					},
				},
			},
		},
		{
			Name:    "gsuite",
			Summary: "Google Suite commands",
			Section: "Google Suite",
			Subcommands: []Command{
				{
					Name:    "login",
					Summary: "Authenticate with Google and choose apps",
					Usage:   "gsuite login",
					Run: func(_ []string) {
						gsuite.RunLogin(dataDir)
					},
				},
				{
					Name:    "apps",
					Summary: "View/toggle enabled Google apps",
					Usage:   "gsuite apps",
					Run: func(_ []string) {
						gsuite.RunApps(dataDir)
					},
				},
				{
					Name:    "reset",
					Summary: "Clear all Google Suite data and token",
					Usage:   "gsuite reset",
					Run: func(_ []string) {
						gsuite.RunReset(dataDir)
					},
				},
			},
		},
		{
			Name:    "browser_history",
			Summary: "Browser history commands",
			Section: "Browser History",
			Subcommands: []Command{
				{
					Name:    "enable",
					Summary: "Enable history indexing for chrome or brave",
					Usage:   "browser_history enable <chrome|brave>",
					ArgChoices: []Choice{
						{Value: "chrome", Summary: "Google Chrome history"},
						{Value: "brave", Summary: "Brave Browser history"},
					},
					Run: func(args []string) {
						browser_history.RunEnable(dataDir, args)
					},
				},
				{
					Name:    "reset",
					Summary: "Clear browser history snapshot and config",
					Usage:   "browser_history reset",
					Run: func(_ []string) {
						browser_history.RunReset(dataDir)
					},
				},
			},
		},
		{
			Name:    "notebook",
			Summary: "Notebook commands",
			Section: "Notebook",
			Subcommands: []Command{
				{
					Name:    "add",
					Summary: "Add a directory to the notebook index",
					Usage:   "notebook add <path>",
					Run: func(args []string) {
						notebook.RunAdd(dataDir, args)
					},
				},
				{
					Name:    "remove",
					Summary: "Remove a directory from the notebook index",
					Usage:   "notebook remove <path>",
					Run: func(args []string) {
						notebook.RunRemove(dataDir, args)
					},
				},
				{
					Name:    "list",
					Summary: "List configured notebook directories",
					Usage:   "notebook list",
					Run: func(_ []string) {
						notebook.RunList(dataDir)
					},
				},
				{
					Name:    "reset",
					Summary: "Clear all notebook configuration and cache",
					Usage:   "notebook reset",
					Run: func(_ []string) {
						notebook.RunReset(dataDir)
					},
				},
			},
		},
		{
			Name:    "login",
			Summary: "Legacy WhatsApp login alias (deprecated)",
			Usage:   "login",
			Section: "Legacy (deprecated)",
			Run: func(args []string) {
				whatsapp.RunLogin(dataDir, args)
			},
		},
	}
}

// dispatchCLI resolves top-level args into a command run, printing usage to stderr and exiting non-zero when input is invalid.
func dispatchCLI(args []string) {
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	if msg := dispatchCommands(commandTree(), args); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		fmt.Fprintln(os.Stderr)
		printUsage()
		os.Exit(1)
	}
}

// dispatchCommands walks one command level, runs the matched handler with remaining args, and returns user-facing usage errors instead of exiting.
func dispatchCommands(commands []Command, args []string) string {
	cmd := findCommand(commands, args[0])
	if cmd == nil {
		return fmt.Sprintf("Unknown command: %s", args[0])
	}
	if len(cmd.Subcommands) == 0 {
		cmd.Run(args[1:])
		return ""
	}
	if len(args) < 2 {
		return fmt.Sprintf("Error: %s subcommand required", cmd.Name)
	}

	subcmd := findCommand(cmd.Subcommands, args[1])
	if subcmd == nil {
		return fmt.Sprintf("Unknown %s subcommand: %s", cmd.Name, args[1])
	}
	subcmd.Run(args[2:])
	return ""
}

// findCommand returns the command metadata for name so dispatch, usage, and completions can share the same lookup path.
func findCommand(commands []Command, name string) *Command {
	for i := range commands {
		if commands[i].Name == name {
			return &commands[i]
		}
	}
	return nil
}

// printUsage renders grouped help to stderr from the command tree so manual use and bad-input paths show the same surface area.
func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: mcpyeahyouknowme <command> [flags]")
	fmt.Fprintln(os.Stderr, "")

	sections := []string{
		"General",
		"Core Daemon",
		"WhatsApp",
		"Google Suite",
		"Browser History",
		"Notebook",
		"Maintenance",
		"Legacy (deprecated)",
	}
	linesBySection := make(map[string][]string)
	for _, cmd := range commandTree() {
		if len(cmd.Subcommands) == 0 {
			linesBySection[cmd.Section] = append(linesBySection[cmd.Section], usageLine(cmd.Usage, cmd.Summary))
			continue
		}
		for _, subcmd := range cmd.Subcommands {
			linesBySection[cmd.Section] = append(linesBySection[cmd.Section], usageLine(subcmd.Usage, subcmd.Summary))
		}
	}

	for _, section := range sections {
		lines := linesBySection[section]
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "%s:\n", section)
		for _, line := range lines {
			fmt.Fprintln(os.Stderr, line)
		}
		fmt.Fprintln(os.Stderr, "")
	}
}

// usageLine formats one aligned help row from a usage string and summary for human-readable CLI output.
func usageLine(usage, summary string) string {
	return fmt.Sprintf("  %-28s %s", usage, summary)
}

// topLevelCommands exposes the root command list so completion generators do not rebuild their own command inventory.
func topLevelCommands() []Command {
	return commandTree()
}

// commandNames extracts command names so shell completion code can suggest only legal tokens.
func commandNames(commands []Command) []string {
	names := make([]string, 0, len(commands))
	for _, cmd := range commands {
		names = append(names, cmd.Name)
	}
	return names
}

// choiceValues extracts argument choice values so shell completion code can suggest constrained non-command inputs.
func choiceValues(choices []Choice) []string {
	values := make([]string, 0, len(choices))
	for _, choice := range choices {
		values = append(values, choice.Value)
	}
	return values
}

// shellCompletionWords returns the supported shell names in the space-delimited format bash completion generation expects.
func shellCompletionWords() string {
	return strings.Join(choiceValues(findCommand(commandTree(), "completions").ArgChoices), " ")
}
