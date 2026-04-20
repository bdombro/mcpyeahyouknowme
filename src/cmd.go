package main

import (
	"fmt"
	"os"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/browser_history"
	"mcpyeahyouknowme/sources/gsuite"
	"mcpyeahyouknowme/sources/notebook"
	"mcpyeahyouknowme/sources/whatsapp"

	"github.com/spf13/cobra"
)

// newRootCmd builds the canonical Cobra command tree for the CLI.
func newRootCmd() *cobra.Command {
	dataDir := core.DataDir()
	root := &cobra.Command{
		Use:           "mcpyeahyouknowme",
		Short:         "Personal data MCP server",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newMcpCmd(),
		newStatusCmd(),
		newCompletionCmd(root),
		newDeprecatedCompletionsCmd(root),
		newCoreCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newReindexCmd(),
		newResetCmd(dataDir),
		newUninstallCmd(),
		newWhatsappCmd(dataDir),
		newGsuiteCmd(dataDir),
		newBrowserHistoryCmd(dataDir),
		newNotebookCmd(dataDir),
	)
	return root
}

// dispatchCLI executes the Cobra CLI, printing errors to stderr and exiting non-zero on failure.
func dispatchCLI(args []string) {
	initLogger()
	root := newRootCmd()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newMcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP server (stdio transport)",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { runMcp() },
	}
}

func newStatusCmd() *cobra.Command {
	var jsonFlag, liveFlag bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show install status and data locations",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			var optArgs []string
			if jsonFlag {
				optArgs = append(optArgs, "--json")
			}
			if liveFlag {
				optArgs = append(optArgs, "--live")
			}
			if err := writeStatus(statusStdout, optArgs); err != nil {
				fmt.Fprintf(statusStderr, "Error: %v\n", err)
				statusExit(1)
			}
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&liveFlag, "live", false, "Refresh every 10 seconds (cannot combine with --json)")
	cmd.MarkFlagsMutuallyExclusive("json", "live")
	return cmd
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate shell autocompletion script",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "bash",
		Short: "Generate autocompletion script for bash",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return root.GenBashCompletion(cmd.OutOrStdout())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "zsh",
		Short: "Generate autocompletion script for zsh",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return root.GenZshCompletion(cmd.OutOrStdout())
		},
	})
	return cmd
}

// newDeprecatedCompletionsCmd registers "completions <shell>" as a hidden deprecated root command
// so existing shell configs with `eval "$(mcpyeahyouknowme completions zsh 2>/dev/null)"` keep working.
func newDeprecatedCompletionsCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:        "completions <bash|zsh>",
		Short:      "Generate autocompletion script (deprecated: use 'completion bash|zsh')",
		Deprecated: "use 'completion bash' or 'completion zsh' instead",
		Args:       cobra.ExactArgs(1),
		ValidArgs:  []string{"bash", "zsh"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell %q; use bash or zsh", args[0])
			}
		},
	}
}

func newCoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "core",
		Short: "Run the daemon process directly (used by LaunchAgent)",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { runCore() },
	}
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the core daemon",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { runStart() },
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the core daemon",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { runStop() },
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the core daemon",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { runRestart() },
	}
}

func newReindexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the search index from scratch",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { runReindex(nil) },
	}
}

func newResetCmd(dataDir string) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Reset all source connections and data",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { resetAllRunner(dataDir) },
	}
}

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Instructions for proper uninstall (use ./scripts/uninstall.sh)",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { runUninstall() },
	}
}

func newWhatsappCmd(dataDir string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whatsapp",
		Short: "WhatsApp commands",
	}
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to WhatsApp (scan QR code)",
		Args:  cobra.NoArgs,
	}
	var relogin bool
	loginCmd.Flags().BoolVar(&relogin, "relogin", false, "Clear existing session and re-pair")
	loginCmd.Run = func(cmd *cobra.Command, args []string) { whatsapp.RunLogin(dataDir, relogin) }
	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Enable WhatsApp syncing",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { whatsapp.RunEnable(dataDir) },
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Disable WhatsApp syncing",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { whatsapp.RunDisable(dataDir) },
		},
		loginCmd,
		&cobra.Command{
			Use:   "reset",
			Short: "Wipe WhatsApp data and session",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { whatsapp.RunReset(dataDir) },
		},
	)
	return cmd
}

// gsuiteApps lists all valid app names accepted by gsuite enable and disable.
var gsuiteApps = []string{"all", "docs", "sheets", "gmail", "calendar", "tasks", "contacts", "slides"}

func newGsuiteCmd(dataDir string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gsuite",
		Short: "Google Suite commands",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:       "enable [all|<app>]",
			Short:     "Enable Google Suite or a specific app",
			Args:      cobra.MaximumNArgs(1),
			ValidArgs: gsuiteApps,
			Run: func(cmd *cobra.Command, args []string) {
				app := ""
				if len(args) > 0 {
					app = args[0]
				}
				gsuite.RunEnable(dataDir, app)
			},
		},
		&cobra.Command{
			Use:       "disable [all|<app>]",
			Short:     "Disable Google Suite or a specific app",
			Args:      cobra.MaximumNArgs(1),
			ValidArgs: gsuiteApps,
			Run: func(cmd *cobra.Command, args []string) {
				app := ""
				if len(args) > 0 {
					app = args[0]
				}
				gsuite.RunDisable(dataDir, app)
			},
		},
		&cobra.Command{
			Use:   "login",
			Short: "Authenticate with Google and choose apps",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { gsuite.RunLogin(dataDir) },
		},
		&cobra.Command{
			Use:   "manage",
			Short: "View/toggle enabled Google apps",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { gsuite.RunManage(dataDir) },
		},
		&cobra.Command{
			Use:        "apps",
			Short:      "Deprecated alias for manage",
			Deprecated: "use 'manage' instead",
			Args:       cobra.NoArgs,
			Run:        func(cmd *cobra.Command, args []string) { gsuite.RunManage(dataDir) },
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Clear all Google Suite data and token",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { gsuite.RunReset(dataDir) },
		},
	)
	return cmd
}

func newBrowserHistoryCmd(dataDir string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser_history",
		Short: "Browser history commands",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:       "enable [chrome|brave]",
			Short:     "Enable browser history indexing",
			Args:      cobra.MaximumNArgs(1),
			ValidArgs: []string{"chrome", "brave"},
			Run: func(cmd *cobra.Command, args []string) {
				browser := ""
				if len(args) > 0 {
					browser = args[0]
				}
				browser_history.RunEnable(dataDir, browser)
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Disable browser history indexing",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { browser_history.RunDisable(dataDir) },
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Clear browser history snapshot and config",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { browser_history.RunReset(dataDir) },
		},
	)
	return cmd
}

func newNotebookCmd(dataDir string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notebook",
		Short: "Notebook commands",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Enable notebook indexing",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { notebook.RunEnable(dataDir) },
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Disable notebook indexing",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { notebook.RunDisable(dataDir) },
		},
		&cobra.Command{
			Use:   "add <path>",
			Short: "Add a directory to the notebook index",
			Args:  cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				notebook.RunAdd(dataDir, args[0])
			},
		},
		&cobra.Command{
			Use:   "remove <path>",
			Short: "Remove a directory from the notebook index",
			Args:  cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				notebook.RunRemove(dataDir, args[0])
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List configured notebook directories",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { notebook.RunList(dataDir) },
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Clear all notebook configuration and cache",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { notebook.RunReset(dataDir) },
		},
	)
	return cmd
}
