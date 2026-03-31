package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// NewMessageStore opens (or creates) the message and contacts databases.
func NewMessageStore() (*MessageStore, error) {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}

	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", filepath.Join(dir, "messages.db")))
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	// Enable WAL mode and busy timeout to prevent locking issues when core
	// daemon and sync (or two syncs) access the database concurrently.
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);
		
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);

		CREATE TABLE IF NOT EXISTS group_participants (
			group_jid TEXT,
			participant_jid TEXT,
			PRIMARY KEY (group_jid, participant_jid)
		);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	// FTS5 full-text index for BM25 keyword search.
	// External content table backed by messages — kept in sync via triggers.
	db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		content,
		content='messages',
		content_rowid='rowid'
	)`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
	END`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', old.rowid, old.content);
	END`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', old.rowid, old.content);
		INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
	END`)

	// Rebuild FTS index if empty. External content tables return rows from the
	// source table on SELECT, so we probe the actual index with a token query.
	var msgCount int
	db.QueryRow("SELECT COUNT(*) FROM messages WHERE content != ''").Scan(&msgCount)
	if msgCount > 0 {
		var indexed int
		db.QueryRow("SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH '*'").Scan(&indexed)
		if indexed == 0 {
			db.Exec("INSERT INTO messages_fts(messages_fts) VALUES('rebuild')")
		}
	}

	store := &MessageStore{db: db}

	// Open whatsmeow contacts DB (read-only) for name resolution; non-fatal if missing
	contactsDB, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=30000", filepath.Join(dir, "whatsapp.db")))
	if err == nil {
		store.contactsDB = contactsDB
	}

	return store, nil
}
