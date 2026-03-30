package main

import (
	"context"
	"fmt"
	"os"
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
	fmt.Fprintln(os.Stderr, "  install-daemon           Install core as a macOS LaunchAgent")
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
	fmt.Fprintln(os.Stderr, "Maintenance:")
	fmt.Fprintln(os.Stderr, "  uninstall                Remove daemon, data, and binaries")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Legacy (deprecated):")
	fmt.Fprintln(os.Stderr, "  login, reset (use 'whatsapp' prefix for WhatsApp commands)")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// Handle WhatsApp subcommands
	if cmd == "whatsapp" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Error: whatsapp subcommand required")
			printUsage()
			os.Exit(1)
		}
		subcmd := args[0]
		switch subcmd {
		case "login":
			runLogin(args[1:])
			return
		case "reset":
			runWhatsAppReset()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown whatsapp subcommand: %s\n\n", subcmd)
			printUsage()
			os.Exit(1)
		}
	}

	// Handle Google Docs subcommands
	if cmd == "googledocs" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Error: googledocs subcommand required")
			printUsage()
			os.Exit(1)
		}
		subcmd := args[0]
		switch subcmd {
		case "login":
			runGoogleDocsLogin()
			return
		case "reset":
			runGoogleDocsReset()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown googledocs subcommand: %s\n\n", subcmd)
			printUsage()
			os.Exit(1)
		}
	}

	switch cmd {
	// General commands
	case "mcp":
		runMcp()
		return
	case "info":
		runInfo()
		return
	case "completions":
		shell := "zsh"
		if len(args) > 0 {
			shell = args[0]
		}
		runCompletions(shell)
		return

	// Core Daemon commands
	case "core":
		runCore()
		return
	case "install-daemon", "daemon":
		runInstallDaemon()
		return
	case "start":
		runStart()
		return
	case "stop":
		runStop()
		return
	case "restart":
		runRestart()
		return

	// Maintenance
	case "uninstall":
		runUninstall()
		return

	// WhatsApp commands (legacy login/reset kept for backward compatibility)
	case "login":
		runLogin(args)
		return
	case "reset":
		runReset()
		return

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

// runCore starts all available data source core services.
// Each data source that implements CoreService will be started.
func runCore() {
	sources, err := LoadSources()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load data sources: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		for _, src := range sources {
			src.Close()
		}
	}()

	// Filter to sources that implement CoreService
	var coreServices []struct {
		name string
		svc  CoreService
	}

	for _, src := range sources {
		if coreSvc, ok := src.(CoreService); ok {
			// Skip sources that require auth if not authenticated
			if coreSvc.RequiresAuth() && !isSourceAuthenticated(src) {
				fmt.Printf("ℹ %s core service requires authentication - run 'mcpyeahyouknowme %s login' first\n",
					src.Description(), src.Name())
				continue
			}
			coreServices = append(coreServices, struct {
				name string
				svc  CoreService
			}{
				name: src.Name(),
				svc:  coreSvc,
			})
		}
	}

	if len(coreServices) == 0 {
		fmt.Println("No data source core services available to run.")
		os.Exit(1)
	}

	// Run all core services concurrently
	fmt.Printf("Starting %d core service(s)...\n", len(coreServices))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a channel to wait for all services to start or fail
	errChan := make(chan error, len(coreServices))

	for _, cs := range coreServices {
		fmt.Printf("  - %s\n", cs.name)
		go func(name string, svc CoreService) {
			if err := svc.StartCore(ctx); err != nil {
				errChan <- fmt.Errorf("%s: %w", name, err)
			} else {
				errChan <- nil
			}
		}(cs.name, cs.svc)
	}

	// Wait for the first error or signal
	for i := 0; i < len(coreServices); i++ {
		if err := <-errChan; err != nil {
			fmt.Fprintf(os.Stderr, "Core service error: %v\n", err)
			cancel()
			os.Exit(1)
		}
	}
}

// isSourceAuthenticated checks if a data source is authenticated.
func isSourceAuthenticated(src DataSource) bool {
	switch src.Name() {
	case "whatsapp":
		return isLoggedIn()
	case "googledocs":
		if gd, ok := src.(*GoogleDocsSource); ok {
			return gd.isAuthenticated()
		}
		return false
	default:
		// Other sources assumed not to need auth unless they implement CoreService.RequiresAuth()
		return true
	}
}
