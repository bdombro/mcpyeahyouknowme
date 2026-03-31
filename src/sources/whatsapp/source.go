package whatsapp

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"mcpyeahyouknowme/core"
)

// Source implements core.DataSource and core.CoreService for WhatsApp.
type Source struct {
	store   *MessageStore
	svc     *MCPService
	dataDir string
}

// NewSource creates a WhatsApp source rooted at dataDir.
func NewSource(dataDir string) *Source {
	store, err := NewMessageStore(dataDir)
	if err != nil {
		// Return a source with nil store; MCP tools will return empty results.
		store = &MessageStore{db: &sql.DB{}}
	}
	svc := NewMCPService(store, "http://localhost:8080/api")
	return &Source{store: store, svc: svc, dataDir: dataDir}
}

// NewSourceFromStore creates a Source from existing store and API URL.
// Used by tests to inject in-memory databases and mock servers.
func NewSourceFromStore(store *MessageStore, apiURL string) *Source {
	return &Source{store: store, svc: NewMCPService(store, apiURL)}
}

func (w *Source) Name() string        { return "whatsapp" }
func (w *Source) Description() string { return "WhatsApp" }
func (w *Source) Close() error        { return w.store.Close() }

// Reset removes all WhatsApp data files. Called by the daemon after stopping
// StartCore, or by the CLI when the daemon is not running.
func (w *Source) Reset(dataDir string) error {
	return core.DefaultReset(dataDir, []string{
		"messages.db",
		"messages.db-wal",
		"messages.db-shm",
		"whatsapp.db",
		"whatsapp.db-wal",
		"whatsapp.db-shm",
	})
}

// SearchEntries returns all indexable content for the global search index.
func (w *Source) SearchEntries() ([]core.SearchEntry, error) {
	var entries []core.SearchEntry
	src := w.Name()

	// Chat names
	chatRows, err := w.store.db.Query("SELECT jid, name, last_message_time FROM chats")
	if err == nil {
		defer chatRows.Close()
		for chatRows.Next() {
			var jid string
			var name sql.NullString
			var lastTime sql.NullString
			if chatRows.Scan(&jid, &name, &lastTime) != nil || !name.Valid || name.String == "" {
				continue
			}
			meta, _ := json.Marshal(map[string]interface{}{
				"jid":      jid,
				"is_group": strings.HasSuffix(jid, "@g.us"),
			})
			var ts *time.Time
			if lastTime.Valid {
				t := parseTime(lastTime.String)
				ts = &t
			}
			entries = append(entries, core.SearchEntry{
				Source:      src,
				SourceID:    jid,
				ContentType: "chat_name",
				Title:       name.String,
				Content:     name.String,
				Metadata:    meta,
				Timestamp:   ts,
			})
		}
	}

	// Participants (from whatsmeow_contacts if available)
	if w.store.contactsDB != nil {
		contactRows, err := w.store.contactsDB.Query("SELECT their_jid, full_name, push_name FROM whatsmeow_contacts")
		if err == nil {
			defer contactRows.Close()
			for contactRows.Next() {
				var jid string
				var fullName, pushName sql.NullString
				if contactRows.Scan(&jid, &fullName, &pushName) != nil {
					continue
				}
				if strings.HasSuffix(jid, "@g.us") {
					continue
				}
				displayName := nullStr(fullName)
				if displayName == "" {
					displayName = nullStr(pushName)
				}
				if displayName == "" {
					continue
				}
				phone := jidPhone(jid)
				content := displayName
				if phone != displayName {
					content = displayName + " " + phone
				}

				var groups []string
				gpRows, gpErr := w.store.db.Query(
					"SELECT group_jid FROM group_participants WHERE participant_jid = ?", jid)
				if gpErr == nil {
					for gpRows.Next() {
						var gj string
						if gpRows.Scan(&gj) == nil {
							groups = append(groups, gj)
						}
					}
					gpRows.Close()
				}

				meta, _ := json.Marshal(map[string]interface{}{
					"jid":    jid,
					"groups": groups,
				})
				entries = append(entries, core.SearchEntry{
					Source:      src,
					SourceID:    jid,
					ContentType: "participant",
					Title:       displayName,
					Content:     content,
					Metadata:    meta,
				})
			}
		}
	}

	// Messages (only those with meaningful content)
	msgRows, err := w.store.db.Query(`
		SELECT m.id, m.chat_jid, m.sender, m.content, m.timestamp, m.is_from_me, c.name
		FROM messages m
		JOIN chats c ON m.chat_jid = c.jid
		WHERE LENGTH(m.content) > 20`)
	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var id, chatJID, sender, content, tsStr string
			var isFromMe bool
			var chatName sql.NullString
			if msgRows.Scan(&id, &chatJID, &sender, &content, &tsStr, &isFromMe, &chatName) != nil {
				continue
			}
			ts := parseTime(tsStr)
			meta, _ := json.Marshal(map[string]interface{}{
				"message_id": id,
				"chat_jid":   chatJID,
				"sender":     sender,
				"timestamp":  ts.Format(time.RFC3339),
				"is_from_me": isFromMe,
			})
			entries = append(entries, core.SearchEntry{
				Source:      src,
				SourceID:    id + ":" + chatJID,
				ContentType: "message",
				Title:       nullStr(chatName),
				Content:     content,
				Metadata:    meta,
				Timestamp:   &ts,
			})
		}
	}

	return entries, nil
}
