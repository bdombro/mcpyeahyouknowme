package whatsapp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ---------- Data types returned by MCP tools ----------

// MCPMessage is a WhatsApp message as returned by MCP tools.
type MCPMessage struct {
	Timestamp time.Time `json:"timestamp"`
	Sender    string    `json:"sender"`
	ChatName  string    `json:"chat_name,omitempty"`
	Content   string    `json:"content"`
	IsFromMe  bool      `json:"is_from_me"`
	ChatJID   string    `json:"chat_jid"`
	ID        string    `json:"id"`
	MediaType string    `json:"media_type,omitempty"`
}

// MCPChat is a WhatsApp chat as returned by MCP tools.
type MCPChat struct {
	JID             string     `json:"jid"`
	Name            string     `json:"name"`
	LastMessageTime *time.Time `json:"last_message_time,omitempty"`
	LastMessage     string     `json:"last_message,omitempty"`
	LastSender      string     `json:"last_sender,omitempty"`
	LastIsFromMe    bool       `json:"last_is_from_me,omitempty"`
	IsGroup         bool       `json:"is_group"`
}

// MCPContact is a WhatsApp contact as returned by MCP tools.
type MCPContact struct {
	PhoneNumber string `json:"phone_number"`
	Name        string `json:"name"`
	JID         string `json:"jid"`
}

// MCPMessageContext bundles a target message with surrounding context messages.
type MCPMessageContext struct {
	Message MCPMessage   `json:"message"`
	Before  []MCPMessage `json:"before"`
	After   []MCPMessage `json:"after"`
}

// ---------- Service ----------

// MCPService provides all read/write operations needed by MCP tool handlers.
// Read operations go directly to SQLite; write operations proxy through the
// core daemon's REST API.
type MCPService struct {
	store   *MessageStore
	apiURL  string
	httpCli *http.Client
}

// NewMCPService builds the WhatsApp MCP facade, combining direct SQLite reads with REST writes routed through apiURL.
func NewMCPService(store *MessageStore, apiURL string) *MCPService {
	return &MCPService{
		store:   store,
		apiURL:  apiURL,
		httpCli: &http.Client{Timeout: 30 * time.Second},
	}
}

// ---------- Formatting ----------

// formatMessage renders one message into the human-readable MCP text format.
func (s *MCPService) formatMessage(msg MCPMessage) string {
	var b strings.Builder
	ts := msg.Timestamp.Format("2006-01-02 15:04:05")
	if msg.ChatName != "" {
		fmt.Fprintf(&b, "[%s] Chat: %s ", ts, msg.ChatName)
	} else {
		fmt.Fprintf(&b, "[%s] ", ts)
	}
	prefix := ""
	if msg.MediaType != "" {
		prefix = fmt.Sprintf("[%s - Message ID: %s - Chat JID: %s] ", msg.MediaType, msg.ID, msg.ChatJID)
	}
	sender := "Me"
	if !msg.IsFromMe {
		sender = s.store.GetSenderName(msg.Sender)
	}
	fmt.Fprintf(&b, "From: %s: %s%s\n", sender, prefix, msg.Content)
	return b.String()
}

// formatMessages renders a message slice into MCP text output, or a friendly empty-state string.
func (s *MCPService) formatMessages(msgs []MCPMessage) string {
	if len(msgs) == 0 {
		return "No messages to display."
	}
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(s.formatMessage(m))
	}
	return b.String()
}

// ---------- Read: Messages ----------

// ListMessages is the main message-read entrypoint: it chooses FTS when `query` is set, otherwise chronological filters/pagination.
func (s *MCPService) ListMessages(after, before, sender, chatJID, query string, limit, page int, includeContext bool, ctxBefore, ctxAfter int) (string, error) {
	if query != "" {
		return s.bm25MessageSearch(query, limit, chatJID, after, before, sender, includeContext, ctxBefore, ctxAfter)
	}
	return s.listMessagesChronological(after, before, sender, chatJID, limit, page, includeContext, ctxBefore, ctxAfter)
}

// listMessagesChronological loads filtered messages newest-first, optionally expanding surrounding chat context.
func (s *MCPService) listMessagesChronological(after, before, sender, chatJID string, limit, page int, includeContext bool, ctxBefore, ctxAfter int) (string, error) {
	parts := []string{"SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.media_type FROM messages JOIN chats ON messages.chat_jid = chats.jid"}
	var where []string
	var params []interface{}

	if after != "" {
		where = append(where, "messages.timestamp > ?")
		params = append(params, after)
	}
	if before != "" {
		where = append(where, "messages.timestamp < ?")
		params = append(params, before)
	}
	if sender != "" {
		where = append(where, "messages.sender = ?")
		params = append(params, sender)
	}
	if chatJID != "" {
		where = append(where, "messages.chat_jid = ?")
		params = append(params, chatJID)
	}
	if len(where) > 0 {
		parts = append(parts, "WHERE "+strings.Join(where, " AND "))
	}
	parts = append(parts, "ORDER BY messages.timestamp DESC")
	offset := page * limit
	parts = append(parts, "LIMIT ? OFFSET ?")
	params = append(params, limit, offset)

	rows, err := s.store.db.Query(strings.Join(parts, " "), params...)
	if err != nil { // nocov
		return "", fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	msgs := scanMessages(rows)
	if includeContext && len(msgs) > 0 {
		msgs = s.expandContext(msgs, ctxBefore, ctxAfter)
	}
	return s.formatMessages(msgs), nil
}

// bm25MessageSearch runs FTS search, hydrates ranked hits, and optionally expands surrounding chat context.
func (s *MCPService) bm25MessageSearch(query string, limit int, chatJID, after, before, sender string, includeContext bool, ctxBefore, ctxAfter int) (string, error) {
	ranked := s.bm25Search(query, limit*5, chatJID, after, before)

	if len(ranked) == 0 {
		return s.formatMessages(nil), nil
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	loaded, err := s.loadRankedMessages(ranked)
	if err != nil {
		// nocov
		return "", err
	}

	var msgs []MCPMessage
	for _, r := range ranked {
		msg, ok := loaded[messageKey(r.msgID, r.chatJID)]
		if !ok {
			// nocov
			continue
		}
		if sender != "" && msg.Sender != sender {
			continue
		}
		msgs = append(msgs, msg)
	}

	if includeContext && len(msgs) > 0 {
		msgs = s.expandContext(msgs, ctxBefore, ctxAfter)
	}
	return s.formatMessages(msgs), nil
}

type searchResult struct {
	msgID   string
	chatJID string
	score   float64
}

// bm25Search returns ranked message IDs from the FTS table, with optional chat and time filters.
func (s *MCPService) bm25Search(query string, limit int, chatJID, after, before string) []searchResult {
	safeQuery := strings.ReplaceAll(query, `"`, `""`)
	ftsQuery := `"` + safeQuery + `"`

	parts := []string{`
		SELECT m.id, m.chat_jid, bm25(messages_fts) as score
		FROM messages_fts
		JOIN messages m ON messages_fts.rowid = m.rowid
		WHERE messages_fts MATCH ?`}
	params := []interface{}{ftsQuery}

	if chatJID != "" {
		parts = append(parts, "AND m.chat_jid = ?")
		params = append(params, chatJID)
	}
	if after != "" {
		parts = append(parts, "AND m.timestamp > ?")
		params = append(params, after)
	}
	if before != "" {
		parts = append(parts, "AND m.timestamp < ?")
		params = append(params, before)
	}
	parts = append(parts, "ORDER BY bm25(messages_fts) LIMIT ?")
	params = append(params, limit)

	rows, err := s.store.db.Query(strings.Join(parts, " "), params...)
	if err != nil { // nocov
		return nil
	}
	defer rows.Close()

	var results []searchResult
	for rows.Next() {
		var r searchResult
		if err := rows.Scan(&r.msgID, &r.chatJID, &r.score); err == nil {
			results = append(results, r)
		}
	}
	return results
}

// Loads ranked message rows in one query so BM25 hydration avoids per-result lookups while preserving caller-side ordering.
func (s *MCPService) loadRankedMessages(ranked []searchResult) (map[string]MCPMessage, error) {
	if len(ranked) == 0 {
		return map[string]MCPMessage{}, nil
	}

	placeholders := make([]string, len(ranked))
	params := make([]interface{}, 0, len(ranked)*2)
	for i, r := range ranked {
		placeholders[i] = "(?, ?)"
		params = append(params, r.msgID, r.chatJID)
	}

	rows, err := s.store.db.Query(`
		SELECT messages.timestamp, messages.sender, chats.name, messages.content,
			   messages.is_from_me, chats.jid, messages.id, messages.media_type
		FROM messages
		JOIN chats ON messages.chat_jid = chats.jid
		WHERE (messages.id, messages.chat_jid) IN (VALUES `+strings.Join(placeholders, ",")+`)`,
		params...)
	if err != nil { // nocov
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	loaded := make(map[string]MCPMessage, len(ranked))
	for _, msg := range scanMessages(rows) {
		loaded[messageKey(msg.ID, msg.ChatJID)] = msg
	}
	return loaded, nil
}

// ---------- Read: Message Context ----------

// GetMessageContext loads `messageID` plus before/after neighbors from the same chat so callers can inspect local conversation context.
func (s *MCPService) GetMessageContext(messageID string, beforeN, afterN int) (*MCPMessageContext, error) {
	row := s.store.db.QueryRow(`
		SELECT messages.timestamp, messages.sender, chats.name, messages.content,
			   messages.is_from_me, chats.jid, messages.id, messages.chat_jid, messages.media_type
		FROM messages
		JOIN chats ON messages.chat_jid = chats.jid
		WHERE messages.id = ?`, messageID)

	var ts, senderVal, chatName, content, chatJID, id string
	var isFromMe bool
	var mediaType sql.NullString
	var chatJIDForCtx string
	if err := row.Scan(&ts, &senderVal, &chatName, &content, &isFromMe, &chatJID, &id, &chatJIDForCtx, &mediaType); err != nil {
		return nil, fmt.Errorf("message %s not found: %w", messageID, err)
	}

	target := MCPMessage{
		Timestamp: parseTime(ts),
		Sender:    senderVal,
		ChatName:  chatName,
		Content:   content,
		IsFromMe:  isFromMe,
		ChatJID:   chatJID,
		ID:        id,
		MediaType: nullStr(mediaType),
	}

	beforeMsgs := s.messagesAround(chatJIDForCtx, ts, "< ?", "DESC", beforeN)
	afterMsgs := s.messagesAround(chatJIDForCtx, ts, "> ?", "ASC", afterN)

	return &MCPMessageContext{Message: target, Before: beforeMsgs, After: afterMsgs}, nil
}

// messagesAround returns nearby messages from one chat before or after a timestamp boundary.
func (s *MCPService) messagesAround(chatJID, ts, op, order string, n int) []MCPMessage {
	rows, err := s.store.db.Query(fmt.Sprintf(`
		SELECT messages.timestamp, messages.sender, chats.name, messages.content,
			   messages.is_from_me, chats.jid, messages.id, messages.media_type
		FROM messages
		JOIN chats ON messages.chat_jid = chats.jid
		WHERE messages.chat_jid = ? AND messages.timestamp %s
		ORDER BY messages.timestamp %s
		LIMIT ?`, op, order), chatJID, ts, n)
	if err != nil { // nocov
		return nil
	}
	defer rows.Close()
	return scanMessages(rows)
}

// expandContext replaces each hit with surrounding context messages so MCP callers see local conversation flow.
func (s *MCPService) expandContext(msgs []MCPMessage, before, after int) []MCPMessage {
	if len(msgs) == 0 || (before <= 0 && after <= 0) {
		return msgs
	}

	chats := make(map[string]struct{})
	for _, msg := range msgs {
		if msg.ID == "" || msg.ChatJID == "" {
			continue
		}
		chats[msg.ChatJID] = struct{}{}
	}
	if len(chats) == 0 {
		return msgs
	}

	chatPlaceholders := make([]string, 0, len(chats))
	params := make([]interface{}, 0, len(chats)+len(msgs)*2+2)
	for chatJID := range chats {
		chatPlaceholders = append(chatPlaceholders, "?")
		params = append(params, chatJID)
	}

	targetPlaceholders := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		if msg.ID == "" || msg.ChatJID == "" {
			continue
		}
		targetPlaceholders = append(targetPlaceholders, "(?, ?)")
		params = append(params, msg.ID, msg.ChatJID)
	}
	params = append(params, before, after)

	rows, err := s.store.db.Query(`
		WITH ordered AS (
			SELECT messages.timestamp AS timestamp,
				   messages.sender AS sender,
				   chats.name AS chat_name,
				   messages.content AS content,
				   messages.is_from_me AS is_from_me,
				   chats.jid AS jid,
				   messages.id AS id,
				   messages.media_type AS media_type,
				   messages.chat_jid AS chat_jid,
				   ROW_NUMBER() OVER (PARTITION BY messages.chat_jid ORDER BY messages.timestamp) AS rn
			FROM messages
			JOIN chats ON messages.chat_jid = chats.jid
			WHERE messages.chat_jid IN (`+strings.Join(chatPlaceholders, ",")+`)
		),
		targets AS (
			SELECT id, chat_jid, rn
			FROM ordered
			WHERE (id, chat_jid) IN (VALUES `+strings.Join(targetPlaceholders, ",")+`)
		)
		SELECT ordered.timestamp, ordered.sender, ordered.chat_name, ordered.content,
			   ordered.is_from_me, ordered.jid, ordered.id, ordered.media_type,
			   ordered.chat_jid, ordered.rn, targets.id, targets.chat_jid, targets.rn
		FROM ordered
		JOIN targets ON ordered.chat_jid = targets.chat_jid
			AND ordered.rn BETWEEN targets.rn - ? AND targets.rn + ?
		ORDER BY targets.chat_jid, targets.rn, ordered.rn`,
		params...)
	if err != nil { // nocov
		return msgs
	}
	defer rows.Close()

	type contextRow struct {
		msg      MCPMessage
		rn       int
		targetRN int
	}
	contexts := make(map[string][]contextRow, len(msgs))
	for rows.Next() {
		var ts, sender, chatName, content, chatJID, id string
		var isFromMe bool
		var mediaType sql.NullString
		var rowChatJID string
		var rn int
		var targetID, targetChatJID string
		var targetRN int
		if rows.Scan(&ts, &sender, &chatName, &content, &isFromMe, &chatJID, &id, &mediaType, &rowChatJID, &rn, &targetID, &targetChatJID, &targetRN) != nil { // nocov
			continue
		}
		contexts[messageKey(targetID, targetChatJID)] = append(contexts[messageKey(targetID, targetChatJID)], contextRow{
			msg: MCPMessage{
				Timestamp: parseTime(ts),
				Sender:    sender,
				ChatName:  chatName,
				Content:   content,
				IsFromMe:  isFromMe,
				ChatJID:   chatJID,
				ID:        id,
				MediaType: nullStr(mediaType),
			},
			rn:       rn,
			targetRN: targetRN,
		})
	}

	var expanded []MCPMessage
	for _, msg := range msgs {
		rows := contexts[messageKey(msg.ID, msg.ChatJID)]
		if len(rows) == 0 {
			expanded = append(expanded, msg)
			continue
		}

		beforeMsgs := make([]MCPMessage, 0, before)
		afterMsgs := make([]MCPMessage, 0, after)
		target := msg
		foundTarget := false
		targetRN := rows[0].targetRN
		for _, row := range rows {
			switch {
			case row.rn < targetRN:
				beforeMsgs = append(beforeMsgs, row.msg)
			case row.rn == targetRN:
				target = row.msg
				foundTarget = true
			default:
				afterMsgs = append(afterMsgs, row.msg)
			}
		}
		for i, j := 0, len(beforeMsgs)-1; i < j; i, j = i+1, j-1 {
			beforeMsgs[i], beforeMsgs[j] = beforeMsgs[j], beforeMsgs[i]
		}
		if !foundTarget {
			// nocov
			expanded = append(expanded, msg)
			continue
		}
		expanded = append(expanded, beforeMsgs...)
		expanded = append(expanded, target)
		expanded = append(expanded, afterMsgs...)
	}
	return expanded
}

// Builds a stable composite key for message ID plus chat JID so batched query results can be matched back to ranked/requested items.
func messageKey(messageID, chatJID string) string {
	return messageID + "\x1f" + chatJID
}

// ---------- Read: Chats ----------

// ListChats returns paged chat summaries, optionally fuzzy-filtered by name/participant and optionally including the last message row.
func (s *MCPService) ListChats(query string, limit, page int, includeLast bool, sortBy string) ([]MCPChat, error) {
	parts := []string{`SELECT chats.jid, chats.name, chats.last_message_time,
		messages.content, messages.sender, messages.is_from_me FROM chats`}
	if includeLast {
		parts = append(parts, `LEFT JOIN messages ON chats.jid = messages.chat_jid
			AND chats.last_message_time = messages.timestamp`)
	}

	var where []string
	var params []interface{}

	if query != "" {
		matchingJIDs := s.fuzzyMatchChats(query)
		if len(matchingJIDs) == 0 {
			return nil, nil
		}
		placeholders := make([]string, len(matchingJIDs))
		for i, jid := range matchingJIDs {
			placeholders[i] = "?"
			params = append(params, jid)
		}
		where = append(where, "chats.jid IN ("+strings.Join(placeholders, ",")+")")
	}

	if len(where) > 0 {
		parts = append(parts, "WHERE "+strings.Join(where, " AND "))
	}

	if sortBy == "name" {
		parts = append(parts, "ORDER BY chats.name")
	} else {
		parts = append(parts, "ORDER BY chats.last_message_time DESC")
	}

	offset := page * limit
	parts = append(parts, "LIMIT ? OFFSET ?")
	params = append(params, limit, offset)

	rows, err := s.store.db.Query(strings.Join(parts, " "), params...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var chats []MCPChat
	for rows.Next() {
		var jid string
		var name sql.NullString
		var lastTime sql.NullString
		var lastMsg, lastSender sql.NullString
		var lastFromMe sql.NullBool
		if err := rows.Scan(&jid, &name, &lastTime, &lastMsg, &lastSender, &lastFromMe); err != nil { // nocov
			continue
		}
		chat := MCPChat{
			JID:     jid,
			Name:    nullStr(name),
			IsGroup: strings.HasSuffix(jid, "@g.us"),
		}
		if lastTime.Valid {
			t := parseTime(lastTime.String)
			chat.LastMessageTime = &t
		}
		chat.LastMessage = nullStr(lastMsg)
		chat.LastSender = nullStr(lastSender)
		if lastFromMe.Valid {
			chat.LastIsFromMe = lastFromMe.Bool
		}
		chats = append(chats, chat)
	}
	return chats, nil
}

// fuzzyMatchChats returns chat JIDs whose names, IDs, or participant names fuzzy-match query.
func (s *MCPService) fuzzyMatchChats(query string) []string {
	jids := make(map[string]struct{})

	rows, err := s.store.db.Query("SELECT jid, name FROM chats")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var jid string
			var name sql.NullString
			if rows.Scan(&jid, &name) == nil {
				if fuzzyMatch(query, nullStr(name)) || containsSubstring(toLower(jid), toLower(query)) {
					jids[jid] = struct{}{}
				}
			}
		}
	}

	for _, j := range s.findChatsByParticipantName(query) {
		jids[j] = struct{}{}
	}

	result := make([]string, 0, len(jids))
	for j := range jids {
		result = append(result, j)
	}
	sort.Strings(result)
	return result
}

// findChatsByParticipantName returns chats linked to contacts whose full or push name matches query.
func (s *MCPService) findChatsByParticipantName(query string) []string {
	if s.store.contactsDB == nil {
		return nil
	}

	rows, err := s.store.contactsDB.Query("SELECT their_jid, full_name, push_name FROM whatsmeow_contacts")
	if err != nil { // nocov
		return nil
	}
	defer rows.Close()

	var matchingJIDs []string
	for rows.Next() {
		var jid string
		var fullName, pushName sql.NullString
		if rows.Scan(&jid, &fullName, &pushName) != nil { // nocov
			continue
		}
		if fuzzyMatch(query, nullStr(fullName)) || fuzzyMatch(query, nullStr(pushName)) {
			matchingJIDs = append(matchingJIDs, jid)
		}
	}

	if len(matchingJIDs) == 0 {
		return nil
	}

	var result []string

	placeholders := make([]string, len(matchingJIDs))
	params := make([]interface{}, len(matchingJIDs))
	for i, j := range matchingJIDs {
		placeholders[i] = "?"
		params[i] = j
	}
	gpRows, err := s.store.db.Query(
		"SELECT DISTINCT group_jid FROM group_participants WHERE participant_jid IN ("+strings.Join(placeholders, ",")+")",
		params...)
	if err == nil {
		defer gpRows.Close()
		for gpRows.Next() {
			var gj string
			if gpRows.Scan(&gj) == nil {
				result = append(result, gj)
			}
		}
	}

	result = append(result, matchingJIDs...)
	return result
}

// ---------- Read: Single Chat ----------

// GetChat loads one chat by JID, optionally joining the latest message so callers can show a richer chat summary.
func (s *MCPService) GetChat(chatJID string, includeLast bool) (*MCPChat, error) {
	q := `SELECT c.jid, c.name, c.last_message_time, m.content, m.sender, m.is_from_me
		FROM chats c`
	if includeLast {
		q += ` LEFT JOIN messages m ON c.jid = m.chat_jid AND c.last_message_time = m.timestamp`
	}
	q += ` WHERE c.jid = ?`

	var jid string
	var name, lastTime, lastMsg, lastSender sql.NullString
	var lastFromMe sql.NullBool
	err := s.store.db.QueryRow(q, chatJID).Scan(&jid, &name, &lastTime, &lastMsg, &lastSender, &lastFromMe)
	if err != nil {
		return nil, fmt.Errorf("chat %s not found: %w", chatJID, err)
	}

	chat := MCPChat{JID: jid, Name: nullStr(name), IsGroup: strings.HasSuffix(jid, "@g.us")}
	if lastTime.Valid {
		t := parseTime(lastTime.String)
		chat.LastMessageTime = &t
	}
	chat.LastMessage = nullStr(lastMsg)
	chat.LastSender = nullStr(lastSender)
	if lastFromMe.Valid {
		chat.LastIsFromMe = lastFromMe.Bool
	}
	return &chat, nil
}

// GetDirectChatByContact finds the non-group chat whose JID matches `phone`, returning the latest message metadata when present.
func (s *MCPService) GetDirectChatByContact(phone string) (*MCPChat, error) {
	q := `SELECT c.jid, c.name, c.last_message_time, m.content, m.sender, m.is_from_me
		FROM chats c
		LEFT JOIN messages m ON c.jid = m.chat_jid AND c.last_message_time = m.timestamp
		WHERE c.jid LIKE ? AND c.jid NOT LIKE '%@g.us'
		LIMIT 1`
	var jid string
	var name, lastTime, lastMsg, lastSender sql.NullString
	var lastFromMe sql.NullBool
	err := s.store.db.QueryRow(q, "%"+phone+"%").Scan(&jid, &name, &lastTime, &lastMsg, &lastSender, &lastFromMe)
	if err != nil {
		return nil, fmt.Errorf("no direct chat found for %s: %w", phone, err)
	}
	chat := MCPChat{JID: jid, Name: nullStr(name), IsGroup: false}
	if lastTime.Valid {
		t := parseTime(lastTime.String)
		chat.LastMessageTime = &t
	}
	chat.LastMessage = nullStr(lastMsg)
	chat.LastSender = nullStr(lastSender)
	if lastFromMe.Valid {
		chat.LastIsFromMe = lastFromMe.Bool
	}
	return &chat, nil
}

// GetContactChats returns paged chats where `jid` appeared as sender or direct-chat JID, so callers can pivot from a contact to conversations.
func (s *MCPService) GetContactChats(jid string, limit, page int) ([]MCPChat, error) {
	rows, err := s.store.db.Query(`
		SELECT DISTINCT c.jid, c.name, c.last_message_time,
			m.content, m.sender, m.is_from_me
		FROM chats c
		JOIN messages m ON c.jid = m.chat_jid
		WHERE m.sender = ? OR c.jid = ?
		ORDER BY c.last_message_time DESC
		LIMIT ? OFFSET ?`, jid, jid, limit, page*limit)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()

	var chats []MCPChat
	for rows.Next() {
		var j string
		var name, lastTime, lastMsg, lastSender sql.NullString
		var lastFromMe sql.NullBool
		if rows.Scan(&j, &name, &lastTime, &lastMsg, &lastSender, &lastFromMe) != nil { // nocov
			continue
		}
		chat := MCPChat{JID: j, Name: nullStr(name), IsGroup: strings.HasSuffix(j, "@g.us")}
		if lastTime.Valid {
			t := parseTime(lastTime.String)
			chat.LastMessageTime = &t
		}
		chat.LastMessage = nullStr(lastMsg)
		chat.LastSender = nullStr(lastSender)
		if lastFromMe.Valid {
			chat.LastIsFromMe = lastFromMe.Bool
		}
		chats = append(chats, chat)
	}
	return chats, nil
}

// ---------- Read: Contacts ----------

// SearchContacts searches chats plus whatsmeow contacts for `query`, merging and deduplicating contact-shaped results.
func (s *MCPService) SearchContacts(query string) ([]MCPContact, error) {
	pattern := "%" + query + "%"
	rows, err := s.store.db.Query(`
		SELECT DISTINCT jid, name FROM chats
		WHERE (LOWER(name) LIKE LOWER(?) OR LOWER(jid) LIKE LOWER(?))
			AND jid NOT LIKE '%@g.us'
		ORDER BY name, jid LIMIT 50`, pattern, pattern)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var contacts []MCPContact
	for rows.Next() {
		var jid string
		var name sql.NullString
		if rows.Scan(&jid, &name) != nil { // nocov
			continue
		}
		seen[jid] = struct{}{}
		contacts = append(contacts, MCPContact{
			PhoneNumber: jidPhone(jid),
			Name:        nullStr(name),
			JID:         jid,
		})
	}

	if s.store.contactsDB != nil {
		waRows, err := s.store.contactsDB.Query(`
			SELECT their_jid, full_name, push_name FROM whatsmeow_contacts
			WHERE (LOWER(full_name) LIKE LOWER(?) OR LOWER(push_name) LIKE LOWER(?)
					OR LOWER(their_jid) LIKE LOWER(?))
				AND their_jid NOT LIKE '%@g.us'
			LIMIT 50`, pattern, pattern, pattern)
		if err == nil {
			defer waRows.Close()
			for waRows.Next() {
				var jid string
				var fullName, pushName sql.NullString
				if waRows.Scan(&jid, &fullName, &pushName) != nil { // nocov
					continue
				}
				if _, exists := seen[jid]; exists {
					continue
				}
				seen[jid] = struct{}{}
				n := nullStr(fullName)
				if n == "" {
					n = nullStr(pushName)
				}
				contacts = append(contacts, MCPContact{
					PhoneNumber: jidPhone(jid),
					Name:        n,
					JID:         jid,
				})
			}
		}
	}

	if len(contacts) > 50 {
		contacts = contacts[:50]
	}
	return contacts, nil
}

// ---------- Read: Last Interaction ----------

// GetLastInteraction returns the newest message involving `jid`, formatted for direct display rather than raw JSON.
func (s *MCPService) GetLastInteraction(jid string) (string, error) {
	row := s.store.db.QueryRow(`
		SELECT m.timestamp, m.sender, c.name, m.content, m.is_from_me, c.jid, m.id, m.media_type
		FROM messages m
		JOIN chats c ON m.chat_jid = c.jid
		WHERE m.sender = ? OR c.jid = ?
		ORDER BY m.timestamp DESC
		LIMIT 1`, jid, jid)

	msg, err := scanMessageRow(row)
	if err != nil {
		return "", fmt.Errorf("no interaction found for %s: %w", jid, err)
	}
	return s.formatMessage(msg), nil
}

// ---------- Write: Send / Download (via REST API) ----------

type apiSendRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message,omitempty"`
	MediaPath string `json:"media_path,omitempty"`
}

type apiSendResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type apiDownloadRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

type apiDownloadResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

// SendMessage posts a text send request through the core daemon because MCP never writes directly to SQLite or WhatsApp.
func (s *MCPService) SendMessage(recipient, message string) (bool, string, error) {
	return s.postSend(apiSendRequest{Recipient: recipient, Message: message})
}

// SendFile posts a media send request through the core daemon, with `mediaPath` resolved on the machine running WhatsApp.
func (s *MCPService) SendFile(recipient, mediaPath string) (bool, string, error) {
	return s.postSend(apiSendRequest{Recipient: recipient, MediaPath: mediaPath})
}

// SendAudioMessage posts an audio send request through the core daemon, expecting daemon-side voice-message handling.
func (s *MCPService) SendAudioMessage(recipient, mediaPath string) (bool, string, error) {
	return s.postSend(apiSendRequest{Recipient: recipient, MediaPath: mediaPath})
}

// postSend posts one send request to the core daemon and returns its success/message payload.
func (s *MCPService) postSend(req apiSendRequest) (bool, string, error) {
	body, _ := json.Marshal(req)
	resp, err := s.httpCli.Post(s.apiURL+"/send", "application/json", bytes.NewReader(body))
	if err != nil {
		return false, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r apiSendResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return false, string(data), nil
	}
	return r.Success, r.Message, nil
}

// DownloadMedia downloads media from a message via the daemon REST API.
func (s *MCPService) DownloadMedia(messageID, chatJID string) (string, error) {
	body, _ := json.Marshal(apiDownloadRequest{MessageID: messageID, ChatJID: chatJID})
	resp, err := s.httpCli.Post(s.apiURL+"/download", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r apiDownloadResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}
	if !r.Success {
		return "", fmt.Errorf("download failed: %s", r.Message)
	}
	return r.Path, nil
}

// ---------- Row scanning helpers ----------

// scanMessages scans query rows into MCP messages, skipping malformed rows instead of failing the whole result.
func scanMessages(rows *sql.Rows) []MCPMessage {
	var msgs []MCPMessage
	for rows.Next() {
		var ts, sender, chatName, content, chatJID, id string
		var isFromMe bool
		var mediaType sql.NullString
		if rows.Scan(&ts, &sender, &chatName, &content, &isFromMe, &chatJID, &id, &mediaType) != nil { // nocov
			continue
		}
		msgs = append(msgs, MCPMessage{
			Timestamp: parseTime(ts),
			Sender:    sender,
			ChatName:  chatName,
			Content:   content,
			IsFromMe:  isFromMe,
			ChatJID:   chatJID,
			ID:        id,
			MediaType: nullStr(mediaType),
		})
	}
	return msgs
}

type scannable interface {
	Scan(dest ...interface{}) error
}

// scanMessageRow scans one row-like source into an MCP message for search and context helpers.
func scanMessageRow(row scannable) (MCPMessage, error) {
	var ts, sender, chatName, content, chatJID, id string
	var isFromMe bool
	var mediaType sql.NullString
	err := row.Scan(&ts, &sender, &chatName, &content, &isFromMe, &chatJID, &id, &mediaType)
	if err != nil {
		return MCPMessage{}, err
	}
	return MCPMessage{
		Timestamp: parseTime(ts),
		Sender:    sender,
		ChatName:  chatName,
		Content:   content,
		IsFromMe:  isFromMe,
		ChatJID:   chatJID,
		ID:        id,
		MediaType: nullStr(mediaType),
	}, nil
}
