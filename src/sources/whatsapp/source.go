package whatsapp

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"mcpyeahyouknowme/core"
)

// init registers the whatsapp source name so config normalization keeps a stable entry for it.
func init() {
	core.RegisterKnownSource("whatsapp")
}

// Source implements core.DataSource and core.CoreService for WhatsApp.
type Source struct {
	store               *MessageStore
	svc                 *MCPService
	dataDir             string
	sendMessageMaxRunes int // from mcp config; 0 means use default in RegisterTools
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

// SetSendMessageMaxRunes sets the max rune length for whatsapp_send_message (from MCP config). Values <= 0 are ignored.
func (w *Source) SetSendMessageMaxRunes(maxRunes int) {
	if w == nil || maxRunes <= 0 {
		return
	}
	w.sendMessageMaxRunes = maxRunes
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
	err := w.StreamSearchEntries(func(batch []core.SearchEntry) error {
		entries = append(entries, batch...)
		return nil
	})
	return entries, err
}

// StreamSearchEntries emits WhatsApp chat metadata and transcript chunks in
// bounded batches so daemon indexing avoids one giant in-memory export slice.
func (w *Source) StreamSearchEntries(emit func([]core.SearchEntry) error) error {
	if emit == nil {
		return nil
	}
	entries, err := w.chatNameEntries()
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		if err := emit(entries); err != nil {
			return err
		}
	}
	entries, err = w.participantEntries()
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		if err := emit(entries); err != nil {
			return err
		}
	}
	return w.streamChatContentEntries(emit)
}

// HasChangesSince checks the WhatsApp SQLite files so incremental daemon
// ticks can skip re-indexing when no synced chat data changed.
func (w *Source) HasChangesSince(t time.Time) bool {
	if t.IsZero() {
		return true
	}
	latest := latestWhatsAppDBModTime(w.dataDir)
	if latest.IsZero() {
		return true
	}
	return !latest.Before(t)
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

// chatNameEntries loads the searchable chat-title rows that point global
// search results back to one WhatsApp chat or group JID.
func (w *Source) chatNameEntries() ([]core.SearchEntry, error) {
	var entries []core.SearchEntry
	src := w.Name()
	chatRows, err := w.store.db.Query("SELECT jid, name, last_message_time FROM chats")
	if err != nil {
		return nil, fmt.Errorf("query chats: %w", err)
	}
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
	return entries, nil
}

// participantEntries loads searchable participant rows, including the groups a
// contact belongs to, so name and phone lookups can find the right chat.
// The contacts DB is optional (opened read-only from whatsapp.db which may not
// exist), so query failures are treated as "no contacts" rather than errors.
func (w *Source) participantEntries() ([]core.SearchEntry, error) {
	if w.store.contactsDB == nil {
		return nil, nil
	}
	var entries []core.SearchEntry
	src := w.Name()
	contactRows, err := w.store.contactsDB.Query("SELECT their_jid, full_name, push_name FROM whatsmeow_contacts")
	if err != nil {
		return nil, nil
	}
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
	return entries, nil
}

// streamChatContentEntries emits one chat's transcript chunks at a time so
// long WhatsApp histories do not accumulate all message chunks in memory.
func (w *Source) streamChatContentEntries(emit func([]core.SearchEntry) error) error {
	src := w.Name()
	msgRows, err := w.store.db.Query(`
		SELECT m.id, m.chat_jid, m.sender, m.content, m.timestamp, m.is_from_me, c.name
		FROM messages m
		JOIN chats c ON m.chat_jid = c.jid
		WHERE LENGTH(m.content) > 3
		ORDER BY m.chat_jid, m.timestamp, m.id`)
	if err != nil {
		return fmt.Errorf("query messages: %w", err)
	}
	defer msgRows.Close()

	var chatMessages []whatsAppMessageRecord
	currentChatJID := ""
	flushChat := func() error {
		if len(chatMessages) == 0 {
			return nil
		}
		err := emit(w.chatSearchEntries(src, chatMessages))
		chatMessages = nil
		return err
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
			if err := flushChat(); err != nil {
				return err
			}
		}
		currentChatJID = msg.ChatJID
		chatMessages = append(chatMessages, msg)
	}
	if err := flushChat(); err != nil {
		return err
	}
	return msgRows.Err()
}

// latestWhatsAppDBModTime returns the newest modification time across the
// WhatsApp SQLite files so WAL-backed writes still count as source changes.
func latestWhatsAppDBModTime(dataDir string) time.Time {
	var latest time.Time
	for _, name := range []string{
		"messages.db", "messages.db-wal", "messages.db-shm",
		"whatsapp.db", "whatsapp.db-wal", "whatsapp.db-shm",
	} {
		info, err := os.Stat(filepath.Join(dataDir, name))
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// Builds per-chat transcript chunks so BM25 search can match adjacent WhatsApp messages together.
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
		targetSize = core.ChunkMaxChars * 3 / 4
		maxSize    = core.ChunkMaxChars
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
		currentLen = utf8.RuneCountInString(header) + utf8.RuneCountInString(content)
		chunkStartID, chunkEndID = msg.ID, msg.ID
		chunkStartDate, chunkEndDate = msg.Timestamp, msg.Timestamp
		hasEntries = true
	}

	for _, msg := range messages {
		entry := w.formatChatTranscriptEntry(msg)
		if entry == "" {
			continue
		}
		if utf8.RuneCountInString(header)+utf8.RuneCountInString(entry) > maxSize {
			flush()
			for _, part := range splitChatTranscriptEntry(entry, maxSize-utf8.RuneCountInString(header)) {
				startChunk(msg, part)
				flush()
			}
			continue
		}
		if !hasEntries {
			startChunk(msg, entry)
			continue
		}
		if currentLen+2+utf8.RuneCountInString(entry) > targetSize {
			flush()
			startChunk(msg, entry)
			continue
		}
		current.WriteString("\n\n")
		current.WriteString(entry)
		currentLen += 2 + utf8.RuneCountInString(entry)
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
	body := strings.TrimSpace(strings.ToValidUTF8(msg.Content, ""))
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
	if limit <= 0 || utf8.RuneCountInString(entry) <= limit {
		return []string{entry}
	}
	var parts []string
	remaining := entry
	for utf8.RuneCountInString(remaining) > limit {
		prefix := truncateChatRunes(remaining, limit)
		splitAt := strings.LastIndex(prefix, "\n")
		if splitAt < limit/2 {
			splitAt = strings.LastIndex(prefix, " ")
		}
		if splitAt < limit/2 {
			splitAt = len(prefix)
		}
		parts = append(parts, strings.TrimSpace(remaining[:splitAt]))
		remaining = strings.TrimSpace(remaining[splitAt:])
	}
	if remaining != "" {
		parts = append(parts, remaining)
	}
	return parts
}

// truncateChatRunes caps transcript text by rune count so chat chunking can
// honor chunk-size limits without splitting UTF-8 sequences.
func truncateChatRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
