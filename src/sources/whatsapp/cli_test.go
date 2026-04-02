package whatsapp

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcpyeahyouknowme/core"

	_ "github.com/mattn/go-sqlite3"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Verifies WhatsApp info output reports an empty disabled state before any session or message DB exists.
func TestInfoLines_emptyDir(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) != 3 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "disabled") {
		t.Errorf("first line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "no session") {
		t.Errorf("second line: %q", lines[1])
	}
	if !strings.Contains(lines[2], "no database yet") {
		t.Errorf("third line: %q", lines[2])
	}
}

// Verifies WhatsApp info output reports session identity, DB sizes, and message/chat counts when local data exists.
func TestInfoLines_withSessionAndMessages(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	waPath := filepath.Join(dir, "whatsapp.db")
	msgPath := filepath.Join(dir, "messages.db")

	waDB, err := sql.Open("sqlite3", waPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = waDB.Exec(`CREATE TABLE whatsmeow_device (jid TEXT); INSERT INTO whatsmeow_device VALUES ('user@s.whatsapp.net');`)
	if err != nil {
		t.Fatal(err)
	}
	waDB.Close()

	msgDB, err := sql.Open("sqlite3", msgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = msgDB.Exec(`CREATE TABLE chats (id INTEGER); CREATE TABLE messages (id INTEGER);
		INSERT INTO chats VALUES (1),(2); INSERT INTO messages VALUES (1),(2),(3);`)
	if err != nil {
		t.Fatal(err)
	}
	msgDB.Close()

	lines := InfoLines(dir)
	if len(lines) != 5 {
		t.Fatalf("want 5 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "enabled") {
		t.Errorf("status line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Session DB:") || !strings.Contains(lines[1], "MB") {
		t.Errorf("session size line: %q", lines[1])
	}
	if !strings.Contains(lines[2], "user@s.whatsapp.net") {
		t.Errorf("jid line: %q", lines[2])
	}
	if !strings.Contains(lines[3], "Message DB:") || !strings.Contains(lines[3], "MB") {
		t.Errorf("message size line: %q", lines[3])
	}
	if !strings.Contains(lines[4], "3 across 2 chats") {
		t.Errorf("counts line: %q", lines[4])
	}
}

// Verifies WhatsApp info output reports unreadable message DBs without crashing the status command.
func TestInfoLines_messagesDBUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	waPath := filepath.Join(dir, "whatsapp.db")
	waDB, err := sql.Open("sqlite3", waPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = waDB.Exec(`CREATE TABLE whatsmeow_device (jid TEXT); INSERT INTO whatsmeow_device VALUES ('x@s.whatsapp.net');`)
	if err != nil {
		t.Fatal(err)
	}
	waDB.Close()

	msgPath := filepath.Join(dir, "messages.db")
	if err := os.WriteFile(msgPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	lines := InfoLines(dir)
	if len(lines) != 5 || !strings.Contains(lines[4], "unable to read database") {
		t.Fatalf("lines: %q", lines)
	}
}

// Verifies logout handling disables the source config and notifies the caller so auth can be re-established.
func TestHandleLoggedOut_disablesSource(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}

	notified := false
	handleLoggedOut(dir, waLog.Stdout("Test", "ERROR", false), func() {
		notified = true
	})

	if !notified {
		t.Fatal("expected logged-out handler to notify caller")
	}
	if core.LoadConfig(dir).Sources["whatsapp"].Enabled {
		t.Fatal("expected whatsapp source to be disabled")
	}
}
