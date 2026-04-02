package whatsapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
				Name string `json:"name"`
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

// Verifies SearchEntries emits chat-name, participant, and message entries for globally indexed WhatsApp search.
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
	if types["message"] == 0 {
		t.Error("expected message entries")
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

// Verifies SearchEntries prepends sender names for inbound messages but not for messages authored by the local user.
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
		if e.ContentType != "message" {
			continue
		}
		switch {
		case strings.Contains(e.Content, "How are you doing today?"):
			if !strings.HasPrefix(e.Content, "Alice Smith: ") {
				t.Errorf("expected sender prepend for other's message, got: %s", e.Content)
			}
		case strings.Contains(e.Content, "Family dinner tonight"):
			if !strings.HasPrefix(e.Content, "Alice Smith: ") {
				t.Errorf("expected sender prepend for other's group message, got: %s", e.Content)
			}
		case strings.Contains(e.Content, "long message from myself"):
			if e.Content != "This is a long message from myself" {
				t.Errorf("is_from_me message should not have sender prepended, got: %s", e.Content)
			}
		}
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
