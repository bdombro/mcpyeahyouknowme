package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mcpyeahyouknowme/core"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// IsLoggedIn returns true if a valid WhatsApp session exists in whatsapp.db.
func IsLoggedIn(dataDir string) bool {
	waDB := filepath.Join(dataDir, "whatsapp.db")
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

// RunLogin performs the WhatsApp QR login flow.
// Pass --relogin in args to force a fresh session.
func RunLogin(dataDir string, args []string) {
	relogin := false
	for _, arg := range args {
		if arg == "--relogin" || arg == "-relogin" {
			relogin = true
		}
	}

	if IsLoggedIn(dataDir) && !relogin {
		fmt.Println("Already logged in.")
		return
	}

	if relogin {
		fmt.Println("Re-logging in: clearing existing session...")
		os.Remove(filepath.Join(dataDir, "whatsapp.db"))
		os.Remove(filepath.Join(dataDir, "messages.db"))
	}

	os.MkdirAll(dataDir, 0755)

	logger := waLog.Stdout("Login", "INFO", true)
	dbLog := waLog.Stdout("Database", "INFO", true)

	container, err := sqlstore.New(context.Background(), "sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=30000", filepath.Join(dataDir, "whatsapp.db")), dbLog)
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

	messageStore, err := NewMessageStore(dataDir)
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

	cfg := core.LoadConfig(dataDir)
	cfg.Sources["whatsapp"] = core.SourceConfig{Enabled: true}
	if err := core.SaveConfig(dataDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update config.json: %v\n", err)
	}
}

// RunReset removes all WhatsApp data. If the daemon is running it writes a
// reset flag to config.json and lets the daemon do the cleanup; otherwise it
// calls Reset() directly.
func RunReset(dataDir string) {
	src := NewSource(dataDir)
	if err := src.Reset(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning during reset: %v\n", err)
	}
	fmt.Println("WhatsApp data reset complete.")
}
