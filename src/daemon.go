package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
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

func isLoggedIn() bool {
	waDB := filepath.Join(dataDir(), "whatsapp.db")
	if _, err := os.Stat(waDB); err != nil {
		return false
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=30000", waDB))
	if err != nil {
		return false
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var jid string
	err = db.QueryRowContext(ctx, "SELECT jid FROM whatsmeow_device WHERE jid != '' LIMIT 1").Scan(&jid)
	return err == nil && jid != ""
}

func requireLogin() {
	if !isLoggedIn() {
		fmt.Fprintln(os.Stderr, "Not logged in to WhatsApp. Run 'mcpyeahyouknowme whatsapp login' first.")
		os.Exit(1)
	}
}

func runLogin(args []string) {
	relogin := false
	for _, arg := range args {
		if arg == "--relogin" || arg == "-relogin" {
			relogin = true
		}
	}

	if isLoggedIn() && !relogin {
		fmt.Println("Already logged in.")
		runInfo()
		return
	}

	if relogin {
		fmt.Println("Re-logging in: clearing existing session...")
		// Stop daemon if running so it doesn't interfere
		plist := plistPath()
		if _, err := os.Stat(plist); err == nil {
			exec.Command("launchctl", "unload", plist).Run()
		}
		// Remove only the whatsapp session DB (not messages.db)
		os.Remove(filepath.Join(dataDir(), "whatsapp.db"))
		// Also clear messages.db so fresh history sync replaces stale data
		os.Remove(filepath.Join(dataDir(), "messages.db"))
	}

	dir := dataDir()
	os.MkdirAll(dir, 0755)

	logger := waLog.Stdout("Login", "INFO", true)
	dbLog := waLog.Stdout("Database", "INFO", true)

	container, err := sqlstore.New(context.Background(), "sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=30000", filepath.Join(dir, "whatsapp.db")), dbLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			deviceStore = container.NewDevice()
		} else {
			fmt.Fprintf(os.Stderr, "Error getting device: %v\n", err)
			os.Exit(1)
		}
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client.Store.ID != nil {
		fmt.Println("Already logged in.")
		client.Disconnect()
		return
	}

	// Initialize message store to capture history sync during login
	messageStore, err := NewMessageStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not open message store: %v\n", err)
	}

	fullyConnected := make(chan struct{}, 1)
	var historySyncCount int
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Connected:
			select {
			case fullyConnected <- struct{}{}:
			default:
			}
		case *events.HistorySync:
			if messageStore != nil {
				handleHistorySync(client, messageStore, v, logger)
				historySyncCount++
				fmt.Printf("Received history sync event #%d (%d conversations)\n", historySyncCount, len(v.Data.Conversations))
			}
		}
	})

	qrChan, _ := client.GetQRChannel(context.Background())
	if err := client.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting: %v\n", err)
		os.Exit(1)
	}

	paired := make(chan bool, 1)
	for evt := range qrChan {
		if evt.Event == "code" {
			fmt.Println("\nScan this QR code with your WhatsApp app:")
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
		} else if evt.Event == "success" {
			paired <- true
			break
		}
	}

	select {
	case <-paired:
		fmt.Println("\nPaired! Waiting for connection to stabilize...")
	case <-time.After(3 * time.Minute):
		fmt.Fprintln(os.Stderr, "Timeout waiting for QR code scan")
		os.Exit(1)
	}

	select {
	case <-fullyConnected:
		fmt.Println("Successfully logged in!")
		// Wait for history sync events to arrive and be processed
		fmt.Println("Waiting for initial history sync (up to 60 seconds)...")
		time.Sleep(60 * time.Second)
		if historySyncCount > 0 {
			fmt.Printf("Captured %d history sync event(s) during login.\n", historySyncCount)
		}
	case <-time.After(30 * time.Second):
		fmt.Println("Paired but connection didn't fully establish. Try running 'mcpyeahyouknowme core' to verify.")
	}

	if messageStore != nil {
		messageStore.Close()
	}
	client.Disconnect()

	// Restart daemon if --relogin stopped it
	if relogin {
		plist := plistPath()
		if _, err := os.Stat(plist); err == nil {
			fmt.Println("Restarting core daemon...")
			exec.Command("launchctl", "load", plist).Run()
		}
	}
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

	dDir := dataDir()
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

func runWhatsAppReset() {
	whatsAppReset(dataDir(), plistPath())
}

// whatsAppReset removes WhatsApp databases while preserving the daemon
// installation and other data sources. Accepts paths so tests can use temp dirs.
func whatsAppReset(dDir, plist string) {
	if _, err := os.Stat(dDir); os.IsNotExist(err) {
		fmt.Println("Nothing to reset (data directory does not exist)")
		return
	}

	// Stop daemon before touching files to avoid race conditions with active
	// WhatsApp connections writing to the databases. Don't remove the plist —
	// other services (e.g. Google Docs) should keep running after restart.
	daemonInstalled := false
	if _, err := os.Stat(plist); err == nil {
		daemonInstalled = true
		exec.Command("launchctl", "unload", plist).Run()
		fmt.Println("Stopped core daemon")
	}

	whatsappFiles := []string{
		filepath.Join(dDir, "whatsapp.db"),
		filepath.Join(dDir, "messages.db"),
	}

	for _, file := range whatsappFiles {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", file, err)
		} else if err == nil {
			fmt.Printf("Removed %s\n", file)
		}
	}

	// Restart daemon — it will detect WhatsApp is not logged in and skip
	// WhatsApp services, while continuing to run other sources.
	if daemonInstalled {
		exec.Command("launchctl", "load", plist).Run()
		fmt.Println("Restarted core daemon (WhatsApp disabled)")
	}

	fmt.Println("WhatsApp data reset complete")
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

func runInfo() {
	dDir := dataDir()

	fmt.Println("┌──────────────────────────────────────────┐")
	fmt.Println("│         mcpyeahyouknowme info            │")
	fmt.Println("└──────────────────────────────────────────┘")
	fmt.Println()

	fmt.Println("\U0001f527 Build")
	fmt.Printf("   Version:    %s\n", BuildVersion)
	fmt.Printf("   Built:      %s\n", BuildTime)
	fmt.Println()

	fmt.Println("\U0001f4c1 Data")
	fmt.Printf("   Directory:  %s\n", dDir)
	if info, err := os.Stat(dDir); err == nil && info.IsDir() {
		fmt.Println("   Status:     initialized")
	} else {
		fmt.Println("   Status:     not initialized (run 'mcpyeahyouknowme whatsapp login')")
	}
	fmt.Println()

	fmt.Println("\U0001f4f2 WhatsApp")
	for _, line := range whatsappInfoLines(dDir) {
		fmt.Println(line)
	}
	fmt.Println()

	fmt.Println("\U0001f4c4 Google Docs")
	for _, line := range googleDocsInfoLines(dDir) {
		fmt.Println(line)
	}
	fmt.Println()

	fmt.Println("\u2699\ufe0f  Core Daemon")
	plist := plistPath()
	if _, err := os.Stat(plist); err == nil {
		ctxLC, cancelLC := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelLC()
		out, err := exec.CommandContext(ctxLC, "launchctl", "list", plistName).Output()
		if err == nil && len(out) > 0 {
			fmt.Println("   Status:     running")
		} else {
			fmt.Println("   Status:     installed (not running)")
		}
		fmt.Printf("   Plist:      %s\n", plist)
		fmt.Printf("   Logs:       %s\n", filepath.Join(dDir, "core.log"))
	} else {
		fmt.Println("   Status:     not installed")
	}
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
	"googledocs",
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
    local subcmd="${COMP_WORDS[2]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "mcp info completions core start stop restart uninstall whatsapp googledocs login reset" -- "$cur") )
        return
    fi

    if [[ $COMP_CWORD -eq 2 ]]; then
        case "$cmd" in
            whatsapp)
                COMPREPLY=( $(compgen -W "login reset" -- "$cur") )
                ;;
            googledocs)
                COMPREPLY=( $(compgen -W "login reset" -- "$cur") )
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
    local -a cmds wa_cmds gd_cmds comp_args

    cmds=(
        'mcp:Start the MCP server (stdio transport)'
        'info:Show install status and data locations'
        'completions:Print shell completions (bash or zsh)'
        'core:Start the core daemon (data source services)'
        'start:Start the core daemon'
        'stop:Stop the core daemon'
        'restart:Restart the core daemon'
        'uninstall:Remove daemon, data, and binaries'
        'whatsapp:WhatsApp commands'
        'googledocs:Google Docs commands'
    )
    wa_cmds=(
        'login:Log in to WhatsApp (scan QR code)'
        'reset:Wipe WhatsApp data and session'
    )
    gd_cmds=(
        'login:Authenticate with Google OAuth'
        'reset:Clear Google Docs data and token'
    )
    comp_args=(
        'bash:Bash completions'
        'zsh:Zsh completions'
    )

    if (( CURRENT == 2 )); then
        _describe -t commands 'command' cmds
    elif (( CURRENT == 3 )) && [[ "${words[2]}" == whatsapp ]]; then
        _describe -t wa_commands 'whatsapp command' wa_cmds
    elif (( CURRENT == 3 )) && [[ "${words[2]}" == googledocs ]]; then
        _describe -t gd_commands 'googledocs command' gd_cmds
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
