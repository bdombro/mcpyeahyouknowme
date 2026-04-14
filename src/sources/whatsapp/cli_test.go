package whatsapp

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcpyeahyouknowme/core"

	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

// Verifies WhatsApp info output reports an empty state before any session or message DB exists.
func TestInfoLines_emptyDir(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "no database yet") {
		t.Errorf("first line: %q", lines[0])
	}
}

// Verifies WhatsApp info output includes a login hint when enabled but not logged in.
func TestInfoLines_enabledNotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	lines := InfoLines(dir)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "Hint:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Hint line, got %q", lines)
	}
}

// Verifies WhatsApp info output reports session DB sizes and message/chat counts when local data exists.
func TestInfoLines_withSessionAndMessages(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	waPath := filepath.Join(dir, "whatsapp.db")
	msgPath := filepath.Join(dir, "messages.db")

	waDB, err := sql.Open("sqlite", waPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = waDB.Exec(`CREATE TABLE whatsmeow_device (jid TEXT); INSERT INTO whatsmeow_device VALUES ('user@s.whatsapp.net');`)
	if err != nil {
		t.Fatal(err)
	}
	waDB.Close()

	msgDB, err := sql.Open("sqlite", msgPath)
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
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "DB:") || !strings.Contains(lines[0], "MB") {
		t.Errorf("db size line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "3 across 2 chats") {
		t.Errorf("counts line: %q", lines[1])
	}
	if got := SessionAuthDisplay(dir); got != "user@s.whatsapp.net" {
		t.Fatalf("SessionAuthDisplay = %q", got)
	}
}

// Verifies WhatsApp info output reports unreadable message DBs without crashing the status command.
func TestInfoLines_messagesDBUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	waPath := filepath.Join(dir, "whatsapp.db")
	waDB, err := sql.Open("sqlite", waPath)
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
	if len(lines) != 2 || !strings.Contains(lines[1], "unable to read database") {
		t.Fatalf("lines: %q", lines)
	}
}

// Verifies SessionAuthDisplay returns no without a session database.
func TestSessionAuthDisplay_empty(t *testing.T) {
	if got := SessionAuthDisplay(t.TempDir()); got != "no" {
		t.Fatalf("SessionAuthDisplay = %q", got)
	}
}

// Verifies pairedWhatsAppJID returns an error when whatsapp.db is absent so callers can distinguish missing DB from empty JID.
func TestPairedWhatsAppJID_missingDB(t *testing.T) {
	if _, err := pairedWhatsAppJID(t.TempDir()); err == nil {
		t.Fatal("expected error when whatsapp.db is missing")
	}
}

// Verifies pairedWhatsAppJID returns an error when whatsapp.db exists but is not a readable SQLite session store.
func TestPairedWhatsAppJID_invalidDB(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "whatsapp.db"), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pairedWhatsAppJID(dir); err == nil {
		t.Fatal("expected error for invalid sqlite file")
	}
	if got := SessionAuthDisplay(dir); got != "no" {
		t.Fatalf("SessionAuthDisplay = %q", got)
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

// Verifies RunReset removes WhatsApp DBs, disables the source, and clears stale WhatsApp rows from search.db.
func TestRunReset_clearsSearchRows(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	for _, rel := range []string{
		"messages.db",
		"messages.db-wal",
		"messages.db-shm",
		"whatsapp.db",
		"whatsapp.db-wal",
		"whatsapp.db-shm",
	} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("seed"), 0644); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
	}
	seedWhatsAppSearchIndex(t, dir)

	RunReset(dir)

	for _, rel := range []string{
		"messages.db",
		"messages.db-wal",
		"messages.db-shm",
		"whatsapp.db",
		"whatsapp.db-wal",
		"whatsapp.db-shm",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err = %v", rel, err)
		}
	}
	if core.LoadConfig(dir).Sources["whatsapp"].Enabled {
		t.Fatal("expected whatsapp to be disabled after reset")
	}
	assertSearchSourceCount(t, dir, "whatsapp", 0)
	assertSearchSourceCount(t, dir, "notebook", 1)
}

// seedWhatsAppSearchIndex creates a minimal shared search index so RunReset can verify only WhatsApp rows are cleared.
func seedWhatsAppSearchIndex(t *testing.T, dataDir string) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "search.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE search_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			source_id TEXT NOT NULL,
			content_type TEXT NOT NULL,
			title TEXT,
			content TEXT NOT NULL,
			metadata TEXT,
			timestamp DATETIME,
			UNIQUE(source, source_id, content_type)
		);
		INSERT INTO search_entries (source, source_id, content_type, title, content)
		VALUES
			('whatsapp', 'chat-1', 'chat_name', 'Family Chat', 'Family Chat'),
			('notebook', 'note-1', 'note_title', 'John Thomas', 'John Thomas');
	`); err != nil {
		t.Fatalf("seed search db: %v", err)
	}
}

// Verifies RunEnable sets whatsapp source enabled in config.
func TestRunEnable(t *testing.T) {
	dir := t.TempDir()
	RunEnable(dir)
	cfg := core.LoadConfig(dir)
	if !cfg.Sources["whatsapp"].Enabled {
		t.Fatal("expected whatsapp to be enabled")
	}
}

// Verifies RunDisable sets whatsapp source disabled in config.
func TestRunDisable(t *testing.T) {
	dir := t.TempDir()
	if err := core.SetSourceEnabled(dir, "whatsapp", true); err != nil {
		t.Fatal(err)
	}
	RunDisable(dir)
	cfg := core.LoadConfig(dir)
	if cfg.Sources["whatsapp"].Enabled {
		t.Fatal("expected whatsapp to be disabled")
	}
}

// assertSearchSourceCount checks the remaining search rows for one source after WhatsApp reset mutates the shared index.
func assertSearchSourceCount(t *testing.T, dataDir, source string, want int) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "search.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_entries WHERE source = ?`, source).Scan(&got); err != nil {
		t.Fatalf("count search rows for %s: %v", source, err)
	}
	if got != want {
		t.Fatalf("search row count for %s = %d, want %d", source, got, want)
	}
}
