package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// whatsappInfoLines returns indented lines for the `info` command WhatsApp section.
func whatsappInfoLines(dDir string) []string {
	var lines []string
	waDB := filepath.Join(dDir, "whatsapp.db")
	if _, err := os.Stat(waDB); err == nil {
		db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=30000", waDB))
		if err == nil {
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			var jid string
			err = db.QueryRowContext(ctx, "SELECT jid FROM whatsmeow_device WHERE jid != '' LIMIT 1").Scan(&jid)
			if err == nil && jid != "" {
				lines = append(lines, fmt.Sprintf("   Logged in:  %s", jid))
			} else {
				lines = append(lines, "   Logged in:  no")
			}
		} else {
			lines = append(lines, "   Logged in:  unable to read session db")
		}
	} else {
		lines = append(lines, "   Logged in:  no session (run 'mcpyeahyouknowme whatsapp login')")
	}

	msgDB := filepath.Join(dDir, "messages.db")
	if _, err := os.Stat(msgDB); err != nil {
		lines = append(lines, "   Messages:   no database yet")
	} else {
		db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=30000", msgDB))
		if err != nil {
			lines = append(lines, "   Messages:   unable to read database")
		} else {
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			var chatCount, msgCount int
			errChats := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats").Scan(&chatCount)
			errMsgs := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&msgCount)
			if errChats != nil || errMsgs != nil {
				lines = append(lines, "   Messages:   unable to read database")
			} else {
				lines = append(lines, fmt.Sprintf("   Messages:   %d across %d chats", msgCount, chatCount))
			}
		}
	}
	return lines
}

// googleDocsInfoLines returns indented lines for the `info` command Google Docs section.
func googleDocsInfoLines(dDir string) []string {
	var lines []string
	tokenPath := filepath.Join(dDir, "googledocs_token.json")
	if _, err := os.Stat(tokenPath); err != nil {
		lines = append(lines, "   Logged in:  no (run 'mcpyeahyouknowme googledocs login')")
	} else {
		lines = append(lines, "   Logged in:  yes")
	}

	dbPath := filepath.Join(dDir, "googledocs.db")
	if _, err := os.Stat(dbPath); err != nil {
		lines = append(lines, "   Documents:  no database yet")
		return lines
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=30000", dbPath))
	if err != nil {
		lines = append(lines, "   Documents:  unable to read database")
		return lines
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents").Scan(&n); err != nil {
		lines = append(lines, "   Documents:  unable to count rows")
		return lines
	}
	lines = append(lines, fmt.Sprintf("   Documents:  %d synced", n))
	return lines
}
