package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
)

// Builds a fully wired MCP server with seeded data and a mock HTTP backend for write-tool coverage.
func buildTestMCPServer(t *testing.T) (*server.MCPServer, *httptest.Server) {
	t.Helper()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/send":
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "sent"})
		case "/download":
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "ok", "path": "/tmp/test.jpg"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(apiSrv.Close)

	store := newTestStoreWithContacts(t)
	ws := NewSourceFromStore(store, apiSrv.URL)

	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	ws.RegisterTools(s)
	return s, apiSrv
}

// Invokes one WhatsApp MCP tool and returns its first text payload for success-path assertions.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) string {
	t.Helper()

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	result := s.HandleMessage(context.Background(), mustMarshalToolCall(name, args))
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return extractText(t, data)
}

// Marshals a JSON-RPC tool call so tests can send raw MCP requests without repeating boilerplate.
func mustMarshalToolCall(name string, args map[string]interface{}) json.RawMessage {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": name, "arguments": args},
	}
	data, _ := json.Marshal(msg)
	return data
}

// Extracts the first text payload from a raw MCP response so tests can assert on tool output content.
func extractText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, string(raw))
	}
	if len(resp.Result.Content) == 0 {
		return ""
	}
	return resp.Result.Content[0].Text
}

// Verifies the contact-search tool returns seeded contacts by human-readable name.
func TestMCP_SearchContacts(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_search_contacts", map[string]interface{}{"query": "Alice"})
	requireContains(t, text, "Alice")
}

// Verifies the list-chats tool returns seeded chats without requiring a query filter.
func TestMCP_ListChats(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_chats", map[string]interface{}{})
	requireContains(t, text, "group1@g.us")
}

// Verifies fuzzy chat queries still match seeded chats through the fuzzy-search path.
func TestMCP_ListChats_fuzzyQuery(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_chats", map[string]interface{}{"query": "Famly"})
	requireContains(t, text, "Family Chat")
}

// Verifies chat listing can match group chats through participant-name search when contacts are available.
func TestMCP_ListChats_participantSearch(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_chats", map[string]interface{}{"query": "Charlie"})
	requireContains(t, text, "group2@g.us")
}

// Verifies the get-chat tool returns the seeded chat record for a known chat JID.
func TestMCP_GetChat(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_chat", map[string]interface{}{"chat_jid": "group1@g.us"})
	requireContains(t, text, "Family Chat")
}

// Verifies the get-chat tool reports a readable not-found error for unknown chat JIDs.
func TestMCP_GetChat_notFound(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_chat", map[string]interface{}{"chat_jid": "nonexistent@s.whatsapp.net"})
	requireContains(t, text, "not found")
}

// Verifies the direct-chat lookup tool resolves a contact phone number to the corresponding direct chat.
func TestMCP_GetDirectChatByContact(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_direct_chat_by_contact", map[string]interface{}{"sender_phone_number": "11111"})
	requireContains(t, text, "Alice Smith")
}

// Verifies the contact-chats tool returns chats associated with the requested contact JID.
func TestMCP_GetContactChats(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_contact_chats", map[string]interface{}{"jid": "11111"})
	requireContains(t, text, "11111@s.whatsapp.net")
}

// Verifies the list-messages tool returns seeded messages for the requested chat.
func TestMCP_ListMessages(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_messages", map[string]interface{}{"chat_jid": "group1@g.us"})
	requireContains(t, text, "dinner")
}

// Verifies the list-messages tool can search across chats when given a message query instead of a chat JID.
func TestMCP_ListMessages_search(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_messages", map[string]interface{}{"query": "meeting"})
	requireContains(t, text, "Meeting at 3pm")
}

// Verifies the message-context tool returns surrounding context for a known message ID.
func TestMCP_GetMessageContext(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_message_context", map[string]interface{}{"message_id": "m4"})
	requireContains(t, text, "m4")
}

// Verifies the last-interaction tool returns a non-empty interaction summary for a known contact.
func TestMCP_GetLastInteraction(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_last_interaction", map[string]interface{}{"jid": "11111@s.whatsapp.net"})
	if text == "" {
		t.Error("expected non-empty last interaction")
	}
}

// Verifies the send-message tool reaches the mock backend and returns a success payload.
func TestMCP_SendMessage(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_send_message", map[string]interface{}{"recipient": "11111", "message": "hi"})
	requireContains(t, text, "success")
}

// Verifies send-message rejects text longer than the default MCP cap without calling the daemon.
func TestMCP_SendMessage_messageTooLong(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	long := strings.Repeat("a", core.DefaultWhatsAppSendMaxRunes+1)
	raw := callToolRaw(t, s, "whatsapp_send_message", map[string]interface{}{"recipient": "11111", "message": long})
	requireContains(t, raw, "message exceeds maximum length")
}

// Verifies SetSendMessageMaxRunes raises the allowed outbound length for whatsapp_send_message.
func TestMCP_SendMessage_respectsSetSendMessageMaxRunes(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "sent"})
	}))
	t.Cleanup(apiSrv.Close)
	store := newTestStoreWithContacts(t)
	ws := NewSourceFromStore(store, apiSrv.URL)
	ws.SetSendMessageMaxRunes(2000)
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	ws.RegisterTools(s)
	msg := strings.Repeat("b", 1500)
	text := callTool(t, s, "whatsapp_send_message", map[string]interface{}{"recipient": "11111", "message": msg})
	requireContains(t, text, "success")
}

// Verifies the send-file tool reaches the mock backend and returns a success payload.
func TestMCP_SendFile(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_send_file", map[string]interface{}{"recipient": "11111", "media_path": "/tmp/test.jpg"})
	requireContains(t, text, "success")
}

// Verifies the send-audio-message tool reaches the mock backend and returns a success payload.
func TestMCP_SendAudioMessage(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_send_audio_message", map[string]interface{}{"recipient": "11111", "media_path": "/tmp/test.ogg"})
	requireContains(t, text, "success")
}

// Verifies the download-media tool returns the downloaded file path for a seeded media message.
func TestMCP_DownloadMedia(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_download_media", map[string]interface{}{"message_id": "m6", "chat_jid": "22222@s.whatsapp.net"})
	requireContains(t, text, "file_path")
}

// Verifies tools/list exposes the full expected WhatsApp MCP tool surface.
func TestMCP_ToolsListContainsAllTools(t *testing.T) {
	s, _ := buildTestMCPServer(t)

	listMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	resultIface := s.HandleMessage(context.Background(), listMsg)
	result, _ := json.Marshal(resultIface)

	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnlyHint    *bool `json:"readOnlyHint"`
					DestructiveHint *bool `json:"destructiveHint"`
					IdempotentHint  *bool `json:"idempotentHint"`
				} `json:"annotations"`
				InputSchema struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	json.Unmarshal(result, &resp)

	expectedTools := []string{
		"whatsapp_search_contacts", "whatsapp_list_chats", "whatsapp_get_chat",
		"whatsapp_get_direct_chat_by_contact", "whatsapp_get_contact_chats",
		"whatsapp_list_messages", "whatsapp_get_message_context",
		"whatsapp_get_last_interaction", "whatsapp_send_message", "whatsapp_send_file",
		"whatsapp_send_audio_message", "whatsapp_download_media",
	}

	toolNames := make(map[string]bool)
	for _, tool := range resp.Result.Tools {
		toolNames[tool.Name] = true
	}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("missing tool: %s", name)
		}
	}

	foundGetChat := false
	for _, tool := range resp.Result.Tools {
		if tool.Name != "whatsapp_get_chat" {
			continue
		}
		foundGetChat = true
		if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
			t.Fatal("expected whatsapp_get_chat readOnlyHint=true")
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatal("expected whatsapp_get_chat destructiveHint=false")
		}
		if tool.Annotations.IdempotentHint == nil || !*tool.Annotations.IdempotentHint {
			t.Fatal("expected whatsapp_get_chat idempotentHint=true")
		}
		if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "chat_jid" {
			t.Fatalf("unexpected whatsapp_get_chat required fields: %#v", tool.InputSchema.Required)
		}
		for _, key := range []string{"chat_jid", "include_last_message"} {
			if _, ok := tool.InputSchema.Properties[key]; !ok {
				t.Fatalf("expected whatsapp_get_chat inputSchema.properties[%q]", key)
			}
		}
	}
	if !foundGetChat {
		t.Fatal("expected whatsapp_get_chat in tools list")
	}
}

// Verifies the WhatsApp source satisfies the core data-source contract and reports the expected identity strings.
func TestWhatsAppSource_interface(t *testing.T) {
	store := newTestStore(t)
	ws := NewSourceFromStore(store, "http://localhost:1")
	defer ws.Close()

	if ws.Name() != "whatsapp" {
		t.Errorf("expected name 'whatsapp', got %q", ws.Name())
	}
	if ws.Description() != "WhatsApp" {
		t.Errorf("expected description 'WhatsApp', got %q", ws.Description())
	}

	var _ core.DataSource = ws
}

// Verifies SearchEntries emits chat-name, participant, and chat-content entries for globally indexed WhatsApp search.
func TestWhatsAppSource_SearchEntries(t *testing.T) {
	store := newTestStoreWithContacts(t)
	ws := NewSourceFromStore(store, "http://localhost:1")

	entries, err := ws.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}

	types := make(map[string]int)
	for _, e := range entries {
		types[e.ContentType]++
		if e.Source != "whatsapp" {
			t.Errorf("expected source=whatsapp, got %s", e.Source)
		}
	}

	if types["chat_name"] == 0 {
		t.Error("expected chat_name entries")
	}
	if types["participant"] == 0 {
		t.Error("expected participant entries")
	}
	if types["chat_content"] == 0 {
		t.Error("expected chat_content entries")
	}
}

// Verifies SearchEntries omits participant entries when no contacts DB is attached.
func TestWhatsAppSource_SearchEntries_noContacts(t *testing.T) {
	store := newTestStore(t)
	ws := NewSourceFromStore(store, "http://localhost:1")

	entries, err := ws.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}

	for _, e := range entries {
		if e.ContentType == "participant" {
			t.Error("should have no participant entries without contacts DB")
		}
	}
}

// Verifies StreamSearchEntries propagates emit failures after producing earlier WhatsApp batches.
func TestWhatsAppSource_StreamSearchEntries_emitError(t *testing.T) {
	store := newTestStoreWithContacts(t)
	ws := NewSourceFromStore(store, "http://localhost:1")
	if err := ws.StreamSearchEntries(nil); err != nil {
		t.Fatalf("StreamSearchEntries(nil): %v", err)
	}

	calls := 0
	err := ws.StreamSearchEntries(func([]core.SearchEntry) error {
		calls++
		if calls == 2 {
			return fmt.Errorf("stop")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected emit error")
	}
	if calls < 2 {
		t.Fatalf("expected at least two batches before failure, got %d", calls)
	}
}

// Verifies HasChangesSince checks WhatsApp DB and WAL mtimes so incremental indexing can skip unchanged caches.
func TestWhatsAppSource_HasChangesSince(t *testing.T) {
	dataDir := t.TempDir()
	ws := &Source{dataDir: dataDir}
	if !ws.HasChangesSince(time.Time{}) {
		t.Fatal("expected zero watermark to force indexing")
	}
	if !ws.HasChangesSince(time.Now()) {
		t.Fatal("expected missing WhatsApp files to trigger indexing")
	}

	dbPath := filepath.Join(dataDir, "messages.db")
	walPath := filepath.Join(dataDir, "messages.db-wal")
	if err := os.WriteFile(dbPath, []byte("db"), 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := os.WriteFile(walPath, []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(dbPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes db: %v", err)
	}
	if err := os.Chtimes(walPath, newTime, newTime); err != nil {
		t.Fatalf("chtimes wal: %v", err)
	}

	if ws.HasChangesSince(time.Now()) {
		t.Fatal("expected future watermark to skip unchanged WhatsApp cache")
	}
	if !ws.HasChangesSince(time.Now().Add(-90 * time.Minute)) {
		t.Fatal("expected WAL change to trigger WhatsApp reindex")
	}
}

// Verifies SearchEntries formats chat-content chunks with sender labels for inbound and self-authored messages.
func TestWhatsAppSource_SearchEntries_senderPrepend(t *testing.T) {
	store := newTestStoreWithContacts(t)

	now := time.Now()
	store.db.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"m_self", "11111@s.whatsapp.net", "me",
		"This is a long message from myself", now.Format(time.RFC3339), true)

	ws := NewSourceFromStore(store, "http://localhost:1")
	entries, err := ws.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}

	for _, e := range entries {
		if e.ContentType != "chat_content" {
			continue
		}
		if strings.Contains(e.Content, "How are you doing today?") &&
			!strings.Contains(e.Content, "Alice Smith\nHow are you doing today?") {
			t.Errorf("expected sender label for other's message, got: %s", e.Content)
		}
		if strings.Contains(e.Content, "Family dinner tonight") &&
			!strings.Contains(e.Content, "Alice Smith\nFamily dinner tonight") {
			t.Errorf("expected sender label for other's group message, got: %s", e.Content)
		}
		if strings.Contains(e.Content, "long message from myself") &&
			!strings.Contains(e.Content, "Me\nThis is a long message from myself") {
			t.Errorf("expected self message to use Me sender label, got: %s", e.Content)
		}
	}
}

// Verifies SearchEntries keeps adjacent chat messages in the same chunk so split thoughts remain searchable together.
func TestWhatsAppSource_SearchEntries_combinesAdjacentMessages(t *testing.T) {
	store := newTestStoreWithContacts(t)
	now := time.Now()
	store.db.Exec(`INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)`,
		"split@g.us", "Split Thought", now.Format(time.RFC3339))
	store.db.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"split1", "split@g.us", "11111", "I am from", now.Add(-2*time.Minute).Format(time.RFC3339), false)
	store.db.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"split2", "split@g.us", "11111", "over the rainbow", now.Add(-1*time.Minute).Format(time.RFC3339), false)

	ws := NewSourceFromStore(store, "http://localhost:1")
	entries, err := ws.SearchEntries()
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}

	for _, e := range entries {
		if e.ContentType != "chat_content" || !strings.Contains(e.Content, "Split Thought") {
			continue
		}
		if strings.Contains(e.Content, "I am from") && strings.Contains(e.Content, "over the rainbow") {
			return
		}
	}
	t.Fatal("expected one chat_content entry to include both adjacent messages")
}

// Verifies chat-chunk building splits long chats into multiple indexed transcript entries with stable boundaries.
func TestWhatsAppSource_buildChatChunks_longChat(t *testing.T) {
	ws := NewSourceFromStore(newTestStoreWithContacts(t), "http://localhost:1")
	var messages []whatsAppMessageRecord
	base := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 40; i++ {
		messages = append(messages, whatsAppMessageRecord{
			ID:        fmt.Sprintf("msg-%02d", i),
			ChatJID:   "group1@g.us",
			Sender:    "11111",
			Content:   strings.Repeat("long family planning update ", 6),
			Timestamp: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			ChatName:  "Family Chat",
		})
	}

	chunks := ws.buildChatChunks(messages)
	if len(chunks) < 2 {
		t.Fatalf("expected long chat to produce multiple chunks, got %d", len(chunks))
	}
	if chunks[0].StartMessageID != "msg-00" {
		t.Fatalf("expected first chunk to start with msg-00, got %#v", chunks[0])
	}
	last := chunks[len(chunks)-1]
	if last.EndMessageID != "msg-39" {
		t.Fatalf("expected last chunk to end with msg-39, got %#v", last)
	}
}

// Verifies chat-chunk building splits oversized single messages instead of dropping them from global search.
func TestWhatsAppSource_buildChatChunks_splitLongMessage(t *testing.T) {
	ws := NewSourceFromStore(newTestStoreWithContacts(t), "http://localhost:1")
	messages := []whatsAppMessageRecord{{
		ID:        "huge",
		ChatJID:   "group1@g.us",
		Sender:    "11111",
		Content:   strings.Repeat("chunked content ", 400),
		Timestamp: time.Now().Format(time.RFC3339),
		ChatName:  "Family Chat",
	}}

	chunks := ws.buildChatChunks(messages)
	if len(chunks) < 2 {
		t.Fatalf("expected huge message to split into multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk.StartMessageID != "huge" || chunk.EndMessageID != "huge" {
			t.Fatalf("expected split chunks to point at the same source message, got %#v", chunk)
		}
	}
}

// Verifies chat chunking preserves UTF-8 boundaries and strips invalid bytes
// from malformed message content before search rows are built.
func TestWhatsAppSource_buildChatChunks_preservesUTF8Boundaries(t *testing.T) {
	ws := NewSourceFromStore(newTestStoreWithContacts(t), "http://localhost:1")
	messages := []whatsAppMessageRecord{{
		ID:        "huge",
		ChatJID:   "group1@g.us",
		Sender:    "11111",
		Content:   strings.Repeat("A\u200c", 2200) + string([]byte{0xff}),
		Timestamp: time.Now().Format(time.RFC3339),
		ChatName:  "Family Chat",
	}}

	chunks := ws.buildChatChunks(messages)
	if len(chunks) < 2 {
		t.Fatalf("expected multibyte message to split into multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk.Content) {
			t.Fatalf("expected valid UTF-8 chunk, got %q", chunk.Content)
		}
	}
}

// Verifies splitChatTranscriptEntry honors rune limits without splitting
// multibyte runes in oversized WhatsApp transcript entries.
func TestSplitChatTranscriptEntry_preservesUTF8Boundaries(t *testing.T) {
	entry := "[2024-03-01T10:00:00Z] Alice\n" + strings.Repeat("A\u200c", 120)
	parts := splitChatTranscriptEntry(entry, 50)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts, got %#v", parts)
	}
	for _, part := range parts {
		if !utf8.ValidString(part) {
			t.Fatalf("expected valid UTF-8 part, got %q", part)
		}
		if utf8.RuneCountInString(part) > 50 {
			t.Fatalf("expected part to respect rune limit, got %d runes", utf8.RuneCountInString(part))
		}
	}
}

// Verifies truncateChatRunes handles zero, passthrough, and truncation cases
// so WhatsApp chunking can cap text without corrupting UTF-8.
func TestTruncateChatRunes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
		want  string
	}{
		{name: "zero limit", input: "hello", limit: 0, want: ""},
		{name: "within limit", input: "hello", limit: 8, want: "hello"},
		{name: "truncate multibyte", input: "A\u200cB", limit: 2, want: "A\u200c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateChatRunes(tt.input, tt.limit); got != tt.want {
				t.Fatalf("truncateChatRunes(%q, %d) = %q, want %q", tt.input, tt.limit, got, tt.want)
			}
		})
	}
}

// Verifies chat-search entry building returns nil for empty chats and filters out low-value numeric transcript chunks.
func TestWhatsAppSource_chatSearchEntries_filtersEmptyAndLowValue(t *testing.T) {
	ws := NewSourceFromStore(newTestStoreWithContacts(t), "http://localhost:1")
	if got := ws.chatSearchEntries("whatsapp", nil); got != nil {
		t.Fatalf("expected nil for empty messages, got %#v", got)
	}

	blank := ws.chatSearchEntries("whatsapp", []whatsAppMessageRecord{{
		ID:        "blank",
		ChatJID:   "group1@g.us",
		Sender:    "11111",
		Content:   "   ",
		Timestamp: time.Now().Format(time.RFC3339),
		ChatName:  "Family Chat",
	}})
	if len(blank) != 0 {
		t.Fatalf("expected blank chat transcript to be skipped, got %#v", blank)
	}

	lowValue := ws.chatSearchEntries("whatsapp", []whatsAppMessageRecord{{
		ID:        "numeric",
		ChatJID:   "group1@g.us",
		Sender:    "11111",
		Content:   strings.Repeat("12345 !!! ", 12),
		Timestamp: time.Now().Format(time.RFC3339),
		ChatName:  "Family Chat",
	}})
	if len(lowValue) != 0 {
		t.Fatalf("expected low-value transcript chunk to be filtered, got %#v", lowValue)
	}
}

// Verifies chat chunk headers fall back to the chat JID when no display name is available.
func TestFormatChatChunkHeader_fallbackToJID(t *testing.T) {
	header := formatChatChunkHeader(whatsAppMessageRecord{ChatJID: "12345@s.whatsapp.net"})
	if !strings.Contains(header, "Chat: 12345@s.whatsapp.net") {
		t.Fatalf("expected header to fall back to chat jid, got %q", header)
	}
	if strings.Contains(header, "Chat JID:") {
		t.Fatalf("expected fallback header to avoid duplicate jid line, got %q", header)
	}
}

// Verifies transcript formatting skips blank bodies and falls back to the raw sender when no contact name is usable.
func TestWhatsAppSource_formatChatTranscriptEntry_fallbackSender(t *testing.T) {
	ws := NewSourceFromStore(newTestStore(t), "http://localhost:1")
	if got := ws.formatChatTranscriptEntry(whatsAppMessageRecord{Content: "   "}); got != "" {
		t.Fatalf("expected blank body to be skipped, got %q", got)
	}

	entry := ws.formatChatTranscriptEntry(whatsAppMessageRecord{
		ID:        "phone",
		ChatJID:   "group1@g.us",
		Sender:    "99999",
		Content:   "Fallback sender name",
		Timestamp: time.Now().Format(time.RFC3339),
	})
	if !strings.Contains(entry, "] 99999\nFallback sender name") {
		t.Fatalf("expected transcript to fall back to raw sender, got %q", entry)
	}
}

// Verifies transcript splitting handles passthrough, newline, word, and hard-limit breakpoints.
func TestSplitChatTranscriptEntry_variants(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		limit int
		parts int
	}{
		{name: "passthrough on nonpositive limit", entry: "hello", limit: 0, parts: 1},
		{name: "split on newline", entry: "line1\nline2\nline3", limit: 10, parts: 3},
		{name: "split on space", entry: "alpha beta gamma delta", limit: 11, parts: 2},
		{name: "hard split on long token", entry: strings.Repeat("x", 30), limit: 10, parts: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts := splitChatTranscriptEntry(tc.entry, tc.limit)
			if len(parts) != tc.parts {
				t.Fatalf("expected %d parts, got %d (%#v)", tc.parts, len(parts), parts)
			}
		})
	}
}

// Verifies the get-chat tool returns the expected required-argument error when `chat_jid` is omitted.
func TestMCP_GetChat_missingArg(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callToolRaw(t, s, "whatsapp_get_chat", map[string]interface{}{})
	requireContains(t, text, "chat_jid parameter is required")
}

// Verifies direct-chat lookup reports a readable error when no direct chat exists for the requested contact.
func TestMCP_GetDirectChat_notFound(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callToolRaw(t, s, "whatsapp_get_direct_chat_by_contact", map[string]interface{}{"sender_phone_number": "99999999"})
	requireContains(t, text, "no direct chat")
}

// Verifies the send-message tool returns the expected required-argument error when mandatory fields are omitted.
func TestMCP_SendMessage_missingArgs(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callToolRaw(t, s, "whatsapp_send_message", map[string]interface{}{})
	requireContains(t, text, "recipient parameter is required")
}

// Verifies chatNameEntries returns an error when the DB has been closed so StreamSearchEntries surfaces it.
func TestWhatsAppSource_chatNameEntries_dbError(t *testing.T) {
	store := newTestStore(t)
	ws := NewSourceFromStore(store, "http://localhost:1")
	store.db.Close()

	_, err := ws.chatNameEntries()
	if err == nil {
		t.Fatal("expected error from closed DB in chatNameEntries")
	}
}

// Verifies participantEntries silently returns nil when the contacts DB is inaccessible,
// since the contacts DB is optional and may legitimately be unavailable.
func TestWhatsAppSource_participantEntries_dbError(t *testing.T) {
	store := newTestStoreWithContacts(t)
	ws := NewSourceFromStore(store, "http://localhost:1")
	store.contactsDB.Close()

	entries, err := ws.participantEntries()
	if err != nil {
		t.Fatalf("expected closed contacts DB to be silently ignored, got error: %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries when contacts DB is closed, got %d", len(entries))
	}
}

// Verifies streamChatContentEntries returns an error when the messages DB has been closed.
func TestWhatsAppSource_streamChatContentEntries_dbError(t *testing.T) {
	store := newTestStore(t)
	ws := NewSourceFromStore(store, "http://localhost:1")
	store.db.Close()

	err := ws.streamChatContentEntries(func([]core.SearchEntry) error { return nil })
	if err == nil {
		t.Fatal("expected error from closed DB in streamChatContentEntries")
	}
}
