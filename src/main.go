package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/googledocs"
	"mcpyeahyouknowme/sources/googlesheets"
	"mcpyeahyouknowme/sources/whatsapp"
)

// Build-time variables set via -ldflags
var (
	BuildTime    = "unknown"
	BuildVersion = "dev"
)

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: mcpyeahyouknowme <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "General:")
	fmt.Fprintln(os.Stderr, "  mcp                      Start the MCP server (stdio transport)")
	fmt.Fprintln(os.Stderr, "  info                     Show install status and data locations")
	fmt.Fprintln(os.Stderr, "  completions [shell]      Print shell completions (bash or zsh)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Core Daemon:")
	fmt.Fprintln(os.Stderr, "  core                     Start the core daemon (data source services)")
	fmt.Fprintln(os.Stderr, "  start                    Start the core daemon")
	fmt.Fprintln(os.Stderr, "  stop                     Stop the core daemon")
	fmt.Fprintln(os.Stderr, "  restart                  Restart the core daemon")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "WhatsApp:")
	fmt.Fprintln(os.Stderr, "  whatsapp login [--relogin]   Log in to WhatsApp (scan QR code)")
	fmt.Fprintln(os.Stderr, "  whatsapp reset               Wipe WhatsApp data and session")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Google Docs:")
	fmt.Fprintln(os.Stderr, "  googledocs login             Authenticate with Google OAuth")
	fmt.Fprintln(os.Stderr, "  googledocs reset             Clear Google Docs data and token")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Google Sheets:")
	fmt.Fprintln(os.Stderr, "  googlesheets login           Authenticate with Google OAuth")
	fmt.Fprintln(os.Stderr, "  googlesheets reset           Clear Google Sheets data and token")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Maintenance:")
	fmt.Fprintln(os.Stderr, "  uninstall                Instructions for proper uninstall (use ./scripts/uninstall.sh)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Legacy (deprecated):")
	fmt.Fprintln(os.Stderr, "  login, reset (use 'whatsapp' prefix for WhatsApp commands)")
}

func main() {
	os.Setenv("GO_TOKENIZER", filepath.Join(core.DataDir(), "cache", "tokenizer"))

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	if cmd == "whatsapp" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Error: whatsapp subcommand required")
			printUsage()
			os.Exit(1)
		}
		subcmd := args[0]
		switch subcmd {
		case "login":
			whatsapp.RunLogin(core.DataDir(), args[1:])
			return
		case "reset":
			whatsapp.RunReset(core.DataDir())
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown whatsapp subcommand: %s\n\n", subcmd)
			printUsage()
			os.Exit(1)
		}
	}

	if cmd == "googledocs" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Error: googledocs subcommand required")
			printUsage()
			os.Exit(1)
		}
		subcmd := args[0]
		switch subcmd {
		case "login":
			googledocs.RunLogin(core.DataDir())
			return
		case "reset":
			googledocs.RunReset(core.DataDir())
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown googledocs subcommand: %s\n\n", subcmd)
			printUsage()
			os.Exit(1)
		}
	}

	if cmd == "googlesheets" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Error: googlesheets subcommand required")
			printUsage()
			os.Exit(1)
		}
		subcmd := args[0]
		switch subcmd {
		case "login":
			googlesheets.RunLogin(core.DataDir())
			return
		case "reset":
			googlesheets.RunReset(core.DataDir())
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown googlesheets subcommand: %s\n\n", subcmd)
			printUsage()
			os.Exit(1)
		}
	}

	switch cmd {
	case "mcp":
		runMcp()
	case "info":
		runInfo()
	case "completions":
		shell := "zsh"
		if len(args) > 0 {
			shell = args[0]
		}
		runCompletions(shell)
	case "core":
		runCore()
	case "start":
		runStart()
	case "stop":
		runStop()
	case "restart":
		runRestart()
	case "uninstall":
		runUninstall()
	case "login":
		// Legacy: backward compatibility
		whatsapp.RunLogin(core.DataDir(), args)
	case "reset":
		runReset()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

// runCore starts data source core services with config polling (10s interval).
func runCore() {
	dir := core.DataDir()
	cfg := loadConfig(dir)

	running := map[string]context.CancelFunc{}

	for name, sc := range cfg.Sources {
		if sc.Reset {
			handleReset(dir, name, &cfg)
			continue
		}
		if sc.Enabled {
			startSource(dir, name, running)
		}
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigCh:
			for _, cancel := range running {
				cancel()
			}
			return
		case <-ticker.C:
			newCfg := loadConfig(dir)
			// Handle resets
			for name, sc := range newCfg.Sources {
				if sc.Reset {
					if cancel, ok := running[name]; ok {
						cancel()
						delete(running, name)
					}
					handleReset(dir, name, &newCfg)
				}
			}
			// Start newly-enabled sources
			for name, sc := range newCfg.Sources {
				if sc.Enabled && !sc.Reset && running[name] == nil {
					startSource(dir, name, running)
				}
			}
			// Stop removed/disabled sources
			for name, cancel := range running {
				sc, exists := newCfg.Sources[name]
				if !exists || !sc.Enabled {
					cancel()
					delete(running, name)
				}
			}
			cfg = newCfg
		}
	}
}

// constructSource builds the named source.
func constructSource(name, dir string) core.DataSource {
	switch name {
	case "whatsapp":
		return whatsapp.NewSource(dir)
	case "googledocs":
		return googledocs.NewSource(dir)
	case "googlesheets":
		return googlesheets.NewSource(dir)
	default:
		return nil
	}
}

// startSource constructs the source, checks auth, and starts its CoreService.
func startSource(dir, name string, running map[string]context.CancelFunc) {
	src := constructSource(name, dir)
	if src == nil {
		fmt.Fprintf(os.Stderr, "Warning: unknown source %q\n", name)
		return
	}
	cs, ok := src.(core.CoreService)
	if !ok {
		return
	}
	if cs.RequiresAuth() && !isSourceAuthenticated(src) {
		fmt.Printf("ℹ %s requires authentication - run 'mcpyeahyouknowme %s login' first\n",
			src.Description(), src.Name())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	running[name] = cancel
	go func() {
		if err := cs.StartCore(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Core service %s error: %v\n", name, err)
		}
		delete(running, name)
	}()
}

// handleReset calls source.Reset(), removes the config entry, and saves config.
func handleReset(dir, name string, cfg *core.Config) {
	src := constructSource(name, dir)
	if src != nil {
		if err := src.Reset(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Reset error for %s: %v\n", name, err)
		}
	}
	delete(cfg.Sources, name)
	saveConfig(dir, *cfg)
}

// isSourceAuthenticated checks if a source has valid credentials.
func isSourceAuthenticated(src core.DataSource) bool {
	switch src.Name() {
	case "whatsapp":
		return whatsapp.IsLoggedIn(core.DataDir())
	case "googledocs":
		if gd, ok := src.(*googledocs.Source); ok {
			_ = gd // isAuthenticated is unexported; RequiresAuth checks state
		}
		// Check by loading token
		return googledocs.NewSource(core.DataDir()).RequiresAuth() && googleDocsTokenExists()
	case "googlesheets":
		return googlesheets.NewSource(core.DataDir()).RequiresAuth() && googleSheetsTokenExists()
	default:
		return true
	}
}

func googleDocsTokenExists() bool {
	tokenPath := filepath.Join(core.DataDir(), "googledocs_token.json")
	_, err := os.Stat(tokenPath)
	return err == nil
}

func googleSheetsTokenExists() bool {
	tokenPath := filepath.Join(core.DataDir(), "googlesheets_token.json")
	_, err := os.Stat(tokenPath)
	return err == nil
}

// LoadSources returns all data sources for MCP use (read-only, no auth gate).
func LoadSources(dir string) []core.DataSource {
	return []core.DataSource{
		whatsapp.NewSource(dir),
		googledocs.NewSource(dir),
		googlesheets.NewSource(dir),
	}
}
