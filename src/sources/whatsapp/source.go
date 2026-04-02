package whatsapp

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mcpyeahyouknowme/core"
)

// init registers the whatsapp source name so config normalization keeps a stable entry for it.
func init() {
	core.RegisterKnownSource("whatsapp")
}

// Source implements core.DataSource and core.CoreService for WhatsApp.
type Source struct {
	store   *MessageStore
	svc     *MCPService
	dataDir string
}

// NewSource builds the MCP/core WhatsApp source around dataDir, falling back to a degraded empty-result store if SQLite cannot open.
func NewSource(dataDir string) *Source {
	store, err := NewMessageStore(dataDir)
	if err != nil {
		// Return a source with nil store; MCP tools will return empty results.
		store = &MessageStore{db: &sql.DB{}}
	}
	svc := NewMCPService(store, "http://127.0.0.1:8080/api")
	return &Source{store: store, svc: svc, dataDir: dataDir}
}

// NewSourceFromStore creates a Source from existing store and API URL.
// Used by tests to inject in-memory databases and mock servers.
func NewSourceFromStore(store *MessageStore, apiURL string) *Source {
	return &Source{store: store, svc: NewMCPService(store, apiURL)}
}

// Name returns the source key used for config, registry lookup, and tool prefixes.
func (w *Source) Name() string { return "whatsapp" }

// Description returns the human label shown in CLI and status output.
func (w *Source) Description() string { return "WhatsApp" }

// Close releases the message store connections so callers do not leak SQLite handles.
func (w *Source) Close() error { return w.store.Close() }

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

	// Messages are indexed as bounded per-chat transcript chunks so search can
	// match meaning that spans adjacent WhatsApp messages.
	msgRows, err := w.store.db.Query(`
		SELECT m.id, m.chat_jid, m.sender, m.content, m.timestamp, m.is_from_me, c.name
		FROM messages m
		JOIN chats c ON m.chat_jid = c.jid
		WHERE LENGTH(m.content) > 3
		ORDER BY m.chat_jid, m.timestamp, m.id`)
	if err == nil {
		defer msgRows.Close()
		var chatMessages []whatsAppMessageRecord
		currentChatJID := ""

		flushChat := func() {
			if len(chatMessages) == 0 {
				return
			}
			entries = append(entries, w.chatSearchEntries(src, chatMessages)...)
			chatMessages = nil
		}

		for msgRows.Next() {
			var msg whatsAppMessageRecord
			var isFromMe bool
			var chatName sql.NullString
			if msgRows.Scan(&msg.ID, &msg.ChatJID, &msg.Sender, &msg.Content, &msg.Timestamp, &isFromMe, &chatName) != nil {
				continue
			}
			msg.IsFromMe = isFromMe
			msg.ChatName = nullStr(chatName)

			if currentChatJID != "" && msg.ChatJID != currentChatJID {
				flushChat()
			}
			currentChatJID = msg.ChatJID
			chatMessages = append(chatMessages, msg)
		}
		flushChat()
	}

	return entries, nil
}

type whatsAppMessageRecord struct {
	ID        string
	ChatJID   string
	Sender    string
	Content   string
	Timestamp string
	IsFromMe  bool
	ChatName  string
}

type whatsAppChatChunk struct {
	Content        string
	StartMessageID string
	EndMessageID   string
	StartDate      string
	EndDate        string
}

// Builds per-chat transcript chunks so hybrid search can match adjacent WhatsApp messages together.
func (w *Source) chatSearchEntries(sourceName string, messages []whatsAppMessageRecord) []core.SearchEntry {
	if len(messages) == 0 {
		return nil
	}
	chunks := w.buildChatChunks(messages)
	if len(chunks) == 0 {
		return nil
	}

	chat := messages[0]
	var entries []core.SearchEntry
	for i, chunk := range chunks {
		if core.IsLowValueContent(chunk.Content) {
			continue
		}
		chunkMeta, _ := json.Marshal(map[string]interface{}{
			"chat_jid":         chat.ChatJID,
			"chat_name":        chat.ChatName,
			"chunk_index":      i,
			"start_message_id": chunk.StartMessageID,
			"end_message_id":   chunk.EndMessageID,
			"start_timestamp":  chunk.StartDate,
			"end_timestamp":    chunk.EndDate,
			"is_group":         strings.HasSuffix(chat.ChatJID, "@g.us"),
			"message_count":    len(messages),
		})
		endTime := parseTime(chunk.EndDate)
		entries = append(entries, core.SearchEntry{
			Source:      sourceName,
			SourceID:    fmt.Sprintf("%s#chunk:%03d", chat.ChatJID, i),
			ContentType: "chat_content",
			Title:       chat.ChatName,
			Content:     chunk.Content,
			Metadata:    chunkMeta,
			Timestamp:   &endTime,
		})
	}
	return entries
}

// Builds bounded transcript chunks from chronological chat messages so long conversations stay searchable without oversized rows.
func (w *Source) buildChatChunks(messages []whatsAppMessageRecord) []whatsAppChatChunk {
	const (
		targetSize = 3000
		maxSize    = 5000
	)
	header := formatChatChunkHeader(messages[0])
	var chunks []whatsAppChatChunk
	var current strings.Builder
	currentLen := 0
	var chunkStartID, chunkEndID, chunkStartDate, chunkEndDate string
	hasEntries := false

	flush := func() {
		if !hasEntries {
			return
		}
		chunks = append(chunks, whatsAppChatChunk{
			Content:        strings.TrimSpace(current.String()),
			StartMessageID: chunkStartID,
			EndMessageID:   chunkEndID,
			StartDate:      chunkStartDate,
			EndDate:        chunkEndDate,
		})
		current.Reset()
		currentLen = 0
		chunkStartID, chunkEndID, chunkStartDate, chunkEndDate = "", "", "", ""
		hasEntries = false
	}

	startChunk := func(msg whatsAppMessageRecord, content string) {
		current.Reset()
		current.WriteString(header)
		current.WriteString(content)
		currentLen = len(header) + len(content)
		chunkStartID, chunkEndID = msg.ID, msg.ID
		chunkStartDate, chunkEndDate = msg.Timestamp, msg.Timestamp
		hasEntries = true
	}

	for _, msg := range messages {
		entry := w.formatChatTranscriptEntry(msg)
		if entry == "" {
			continue
		}
		if len(header)+len(entry) > maxSize {
			flush()
			for _, part := range splitChatTranscriptEntry(entry, maxSize-len(header)) {
				startChunk(msg, part)
				flush()
			}
			continue
		}
		if !hasEntries {
			startChunk(msg, entry)
			continue
		}
		if currentLen+2+len(entry) > targetSize {
			flush()
			startChunk(msg, entry)
			continue
		}
		current.WriteString("\n\n")
		current.WriteString(entry)
		currentLen += 2 + len(entry)
		chunkEndID = msg.ID
		chunkEndDate = msg.Timestamp
	}
	flush()
	return chunks
}

// Builds a shared chat header so every transcript chunk keeps enough chat-level context for retrieval.
func formatChatChunkHeader(msg whatsAppMessageRecord) string {
	var b strings.Builder
	chatName := msg.ChatName
	if chatName == "" {
		chatName = msg.ChatJID
	}
	b.WriteString("Chat: ")
	b.WriteString(chatName)
	b.WriteString("\n")
	if msg.ChatJID != "" && msg.ChatJID != chatName {
		b.WriteString("Chat JID: ")
		b.WriteString(msg.ChatJID)
		b.WriteString("\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

// Formats one WhatsApp message as transcript text so chunked search preserves sender and time context around each utterance.
func (w *Source) formatChatTranscriptEntry(msg whatsAppMessageRecord) string {
	body := strings.TrimSpace(msg.Content)
	if body == "" {
		return ""
	}
	ts := parseTime(msg.Timestamp)
	timeLabel := ts.Format(time.RFC3339)
	senderLabel := "Me"
	if !msg.IsFromMe {
		senderLabel = w.store.GetSenderName(msg.Sender)
		if senderLabel == "" || looksLikePhoneNumber(senderLabel) {
			senderLabel = msg.Sender
		}
	}
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(timeLabel)
	b.WriteString("] ")
	b.WriteString(senderLabel)
	b.WriteString("\n")
	b.WriteString(body)
	return b.String()
}

// Splits oversized transcript entries so one unusually long WhatsApp message does not overflow the chat chunk size cap.
func splitChatTranscriptEntry(entry string, limit int) []string {
	if limit <= 0 || len(entry) <= limit {
		return []string{entry}
	}
	var parts []string
	remaining := entry
	for len(remaining) > limit {
		splitAt := strings.LastIndex(remaining[:limit], "\n")
		if splitAt < limit/2 {
			splitAt = strings.LastIndex(remaining[:limit], " ")
		}
		if splitAt < limit/2 {
			splitAt = limit
		}
		parts = append(parts, strings.TrimSpace(remaining[:splitAt]))
		remaining = strings.TrimSpace(remaining[splitAt:])
	}
	if remaining != "" {
		parts = append(parts, remaining)
	}
	return parts
}
