package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestWhatsappInfoLines_emptyDir(t *testing.T) {
	dir := t.TempDir()
	lines := whatsappInfoLines(dir)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "no session") {
		t.Errorf("first line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "no database yet") {
		t.Errorf("second line: %q", lines[1])
	}
}

func TestWhatsappInfoLines_withSessionAndMessages(t *testing.T) {
	dir := t.TempDir()
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

	lines := whatsappInfoLines(dir)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "user@s.whatsapp.net") {
		t.Errorf("jid line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "3 across 2 chats") {
		t.Errorf("counts line: %q", lines[1])
	}
}

func TestWhatsappInfoLines_messagesDBUnreadable(t *testing.T) {
	dir := t.TempDir()
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

	lines := whatsappInfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[1], "unable to read database") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleDocsInfoLines_noArtifacts(t *testing.T) {
	dir := t.TempDir()
	lines := googleDocsInfoLines(dir)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "no (run") {
		t.Errorf("login: %q", lines[0])
	}
	if !strings.Contains(lines[1], "no database yet") {
		t.Errorf("docs: %q", lines[1])
	}
}

func TestGoogleDocsInfoLines_withToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "googledocs_token.json")
	if err := os.WriteFile(tokenPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := googleDocsInfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[0], "yes") || !strings.Contains(lines[1], "no database yet") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleDocsInfoLines_withDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googledocs_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "googledocs.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE documents (id TEXT PRIMARY KEY); INSERT INTO documents VALUES ('a'),('b');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	lines := googleDocsInfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[1], "2 synced") {
		t.Fatalf("lines: %q", lines)
	}
}

func TestGoogleDocsInfoLines_countQueryFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "googledocs_token.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "googledocs.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE other (x INT);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	lines := googleDocsInfoLines(dir)
	if len(lines) != 2 || !strings.Contains(lines[1], "unable to count") {
		t.Fatalf("lines: %q", lines)
	}
}
