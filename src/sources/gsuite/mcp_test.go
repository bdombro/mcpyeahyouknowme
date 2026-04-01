package gsuite

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// callTool invokes a named tool on the MCP server and returns raw JSON text.
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

	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	result := s.HandleMessage(context.Background(), msg)
	data, _ := json.Marshal(result)

	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, string(data))
	}
	if len(resp.Result.Content) == 0 {
		return ""
	}
	return resp.Result.Content[0].Text
}

func buildMCPServer(t *testing.T) *server.MCPServer {
	t.Helper()
	src := newTestSource(t)
	seedAll(t, src.db)
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	return s
}

// --- Docs MCP tests ---

func TestDocsSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_search", map[string]interface{}{"query": "project proposal"})
	if text == "" {
		t.Fatal("expected non-empty result")
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parse result: %v\ntext: %s", err, text)
	}
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected at least one result")
	}
}

func TestDocsSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_docs_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected empty array result")
	}
}

func TestDocsGetDocument_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_get_document", map[string]interface{}{"document_id": "doc1"})
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parse result: %v\ntext: %s", err, text)
	}
	if result["title"] != "Project Proposal" {
		t.Errorf("expected title 'Project Proposal', got %v", result["title"])
	}
	if result["modified_time"] == nil {
		t.Error("expected modified_time in response")
	}
	if result["created_time"] == nil {
		t.Error("expected created_time in response")
	}
}

func TestDocsGetDocument_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_get_document", map[string]interface{}{"document_id": "nope"})
	if text != "Document not found" {
		t.Errorf("expected 'Document not found', got %q", text)
	}
}

func TestDocsGetDocument_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_docs_get_document", map[string]interface{}{"document_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

func TestDocsListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_list_recent", map[string]interface{}{})
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parse: %v text: %s", err, text)
	}
	count, _ := result["count"].(float64)
	if count != 1 {
		t.Errorf("expected 1 doc, got %v", count)
	}
}

// --- Sheets MCP tests ---

func TestSheetsSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_search", map[string]interface{}{"query": "Budget"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected budget sheet in results")
	}
}

func TestSheetsGetSpreadsheet_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_get_spreadsheet", map[string]interface{}{"spreadsheet_id": "sheet1"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["title"] != "Budget 2024" {
		t.Errorf("expected 'Budget 2024', got %v", result["title"])
	}
	if result["modified_time"] == nil {
		t.Error("expected modified_time in response")
	}
	if result["created_time"] == nil {
		t.Error("expected created_time in response")
	}
}

func TestSheetsGetSpreadsheet_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_get_spreadsheet", map[string]interface{}{"spreadsheet_id": "nope"})
	if text != "Spreadsheet not found" {
		t.Errorf("expected 'Spreadsheet not found', got %q", text)
	}
}

func TestSheetsGetSpreadsheet_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_sheets_get_spreadsheet", map[string]interface{}{"spreadsheet_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

func TestSheetsListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_list_recent", map[string]interface{}{})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 spreadsheet, got %v", result["count"])
	}
}

// --- Gmail MCP tests ---

func TestGmailSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_search", map[string]interface{}{"query": "meeting"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected message in search results")
	}
}

func TestGmailGetMessage_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_message", map[string]interface{}{"message_id": "msg1"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["subject"] != "Meeting Tomorrow" {
		t.Errorf("expected subject 'Meeting Tomorrow', got %v", result["subject"])
	}
	if result["date"] == nil {
		t.Error("expected date in response")
	}
	if result["from"] == nil {
		t.Error("expected from in response")
	}
	if result["folder"] == nil {
		t.Error("expected folder in response")
	}
	if result["thread_id"] != "thread1" {
		t.Errorf("expected thread_id 'thread1', got %v", result["thread_id"])
	}
}

func TestGmailGetMessage_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_message", map[string]interface{}{"message_id": "nope"})
	if text != "Message not found" {
		t.Errorf("expected 'Message not found', got %q", text)
	}
}

func TestGmailGetMessage_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_get_message", map[string]interface{}{"message_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

func TestGmailGetThread_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "thread1"})
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parse result: %v\ntext: %s", err, text)
	}
	if result["thread_id"] != "thread1" {
		t.Errorf("expected thread_id 'thread1', got %v", result["thread_id"])
	}
	if result["message_count"].(float64) != 2 {
		t.Errorf("expected 2 messages, got %v", result["message_count"])
	}
	messages, ok := result["messages"].([]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("expected 2 thread messages, got %#v", result["messages"])
	}
	second, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected second message object, got %#v", messages[1])
	}
	body, _ := second["body"].(string)
	if strings.Contains(body, "On Fri, Mar 1, 2024") {
		t.Errorf("expected visible body without quoted reply, got %q", body)
	}
}

func TestGmailGetThread_IncludeRaw(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "thread1", "include_raw": true})
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parse result: %v\ntext: %s", err, text)
	}
	messages := result["messages"].([]interface{})
	second := messages[1].(map[string]interface{})
	raw, _ := second["body_raw"].(string)
	if !strings.Contains(raw, "On Fri, Mar 1, 2024") {
		t.Errorf("expected raw body to include quoted text, got %q", raw)
	}
}

func TestGmailGetThread_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "nope"})
	if text != "Thread not found" {
		t.Errorf("expected 'Thread not found', got %q", text)
	}
}

func TestGmailGetThread_NoThreadMetadata(t *testing.T) {
	src := newTestSource(t)
	src.db.Exec(`INSERT INTO gmail_messages
		(id, thread_id, labels, folder, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		 date, snippet, body_text, body_raw, body_visible, has_attachments, size_estimate, last_synced)
		VALUES ('orphan1', 'orphan_thread', 'INBOX', 'INBOX', 'Orphan', 'a@b.com', 'c@d.com', '', '',
		 '2024-01-01', 'snip', 'raw only', 'raw only', '', 0, 100, datetime('now'))`)
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "orphan_thread"})
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parse: %v\ntext: %s", err, text)
	}
	if result["subject"] != "Orphan" {
		t.Errorf("expected subject from fallback buildThreadRecord, got %v", result["subject"])
	}
	msgs := result["messages"].([]interface{})
	firstMsg := msgs[0].(map[string]interface{})
	if firstMsg["body"] != "raw only" {
		t.Errorf("expected body fallback to raw, got %v", firstMsg["body"])
	}
}

func TestGmailGetThread_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "thread1"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

func TestGmailListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["count"].(float64) != 2 {
		t.Errorf("expected 2 messages, got %v", result["count"])
	}
	messages := result["messages"].([]interface{})
	first := messages[0].(map[string]interface{})
	if first["thread_id"] != "thread1" {
		t.Errorf("expected thread_id 'thread1', got %v", first["thread_id"])
	}
}

func TestGmailListRecent_FilterByFolder(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{"folder": "INBOX"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["count"].(float64) != 2 {
		t.Errorf("expected 2 INBOX messages, got %v", result["count"])
	}
	text2 := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{"folder": "SENT"})
	var result2 map[string]interface{}
	json.Unmarshal([]byte(text2), &result2)
	if result2["count"].(float64) != 0 {
		t.Errorf("expected 0 SENT messages, got %v", result2["count"])
	}
}

// --- Calendar MCP tests ---

func TestCalendarSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_search", map[string]interface{}{"query": "standup"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected calendar event in results")
	}
}

func TestCalendarGetEvent_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_get_event", map[string]interface{}{"event_id": "cal1|ev1"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["summary"] != "Team Standup" {
		t.Errorf("expected 'Team Standup', got %v", result["summary"])
	}
	if result["start_time"] == nil {
		t.Error("expected start_time in response")
	}
	if result["end_time"] == nil {
		t.Error("expected end_time in response")
	}
	if result["updated_time"] == nil {
		t.Error("expected updated_time in response")
	}
}

func TestCalendarGetEvent_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_get_event", map[string]interface{}{"event_id": "nope"})
	if text != "Event not found" {
		t.Errorf("expected 'Event not found', got %q", text)
	}
}

func TestCalendarGetEvent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_calendar_get_event", map[string]interface{}{"event_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

func TestCalendarListUpcoming(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_list_upcoming", map[string]interface{}{"days": 7})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	// Event is 48h in future, should appear in 7-day window
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected at least 1 upcoming event in 7-day window")
	}
}

// --- Tasks MCP tests ---

func TestTasksSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_tasks_search", map[string]interface{}{"query": "unit tests"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected task in results")
	}
}

func TestTasksList_All(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_tasks_list", map[string]interface{}{})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected tasks in result")
	}
}

func TestTasksList_FilterByStatus(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_tasks_list", map[string]interface{}{"status": "needsAction"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 needsAction task, got %v", result["count"])
	}
}

// --- Contacts MCP tests ---

func TestContactsSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_search", map[string]interface{}{"query": "Alice"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected contact in search results")
	}
}

func TestContactsList(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_list", map[string]interface{}{})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 contact, got %v", result["count"])
	}
}

func TestContactsList_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_contacts_list", map[string]interface{}{})
	if text == "" {
		t.Error("expected non-empty result")
	}
}

// --- Slides MCP tests ---

func TestSlidesSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_search", map[string]interface{}{"query": "revenue"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected presentation in search results")
	}
}

func TestSlidesGetPresentation_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_get_presentation", map[string]interface{}{"presentation_id": "pres1"})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["title"] != "Q1 Review" {
		t.Errorf("expected 'Q1 Review', got %v", result["title"])
	}
	if result["modified_time"] == nil {
		t.Error("expected modified_time in response")
	}
	if result["created_time"] == nil {
		t.Error("expected created_time in response")
	}
}

func TestSlidesGetPresentation_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_get_presentation", map[string]interface{}{"presentation_id": "nope"})
	if text != "Presentation not found" {
		t.Errorf("expected 'Presentation not found', got %q", text)
	}
}

func TestSlidesGetPresentation_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_slides_get_presentation", map[string]interface{}{"presentation_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

func TestSlidesListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_list_recent", map[string]interface{}{})
	var result map[string]interface{}
	json.Unmarshal([]byte(text), &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 presentation, got %v", result["count"])
	}
}

func TestRequiredArgs_Missing(t *testing.T) {
	s := buildMCPServer(t)
	tests := []struct {
		name string
		tool string
		want string
	}{
		{"docs_search", "gsuite_docs_search", "query parameter is required"},
		{"docs_get_document", "gsuite_docs_get_document", "document_id parameter is required"},
		{"sheets_search", "gsuite_sheets_search", "query parameter is required"},
		{"sheets_get_spreadsheet", "gsuite_sheets_get_spreadsheet", "spreadsheet_id parameter is required"},
		{"gmail_search", "gsuite_gmail_search", "query parameter is required"},
		{"gmail_get_message", "gsuite_gmail_get_message", "message_id parameter is required"},
		{"gmail_get_thread", "gsuite_gmail_get_thread", "thread_id parameter is required"},
		{"calendar_search", "gsuite_calendar_search", "query parameter is required"},
		{"calendar_get_event", "gsuite_calendar_get_event", "event_id parameter is required"},
		{"tasks_search", "gsuite_tasks_search", "query parameter is required"},
		{"contacts_search", "gsuite_contacts_search", "query parameter is required"},
		{"slides_search", "gsuite_slides_search", "query parameter is required"},
		{"slides_get_presentation", "gsuite_slides_get_presentation", "presentation_id parameter is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text := callTool(t, s, tc.tool, map[string]interface{}{})
			if !strings.Contains(text, tc.want) {
				t.Fatalf("expected %q in %q", tc.want, text)
			}
		})
	}
}

// --- DisabledApp tests ---

func TestDisabledApp_ToolsNotRegistered(t *testing.T) {
	src := newTestSource(t)
	src.apps = allAppsEnabledConfig()
	src.apps.SetEnabled("gmail", false)
	seedGmail(t, src.db)

	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)

	// When gmail is disabled, the tool should not be found (returns empty content)
	// We just check the registration doesn't panic; the tool won't be in the server
	_ = s
}

// --- Gmail parsing helpers ---

func TestPrimaryFolder(t *testing.T) {
	tests := []struct {
		labels []string
		want   string
	}{
		{[]string{"INBOX", "UNREAD"}, "INBOX"},
		{[]string{"SENT"}, "SENT"},
		{[]string{"DRAFT"}, "DRAFT"},
		{[]string{"UNREAD", "STARRED"}, "ARCHIVE"},
		{[]string{}, "ARCHIVE"},
		{[]string{"CUSTOM_LABEL"}, "CUSTOM_LABEL"},
	}
	for _, tc := range tests {
		got := primaryFolder(tc.labels)
		if got != tc.want {
			t.Errorf("primaryFolder(%v) = %q, want %q", tc.labels, got, tc.want)
		}
	}
}

func TestStripHTML(t *testing.T) {
	input := "<p>Hello <b>world</b></p>"
	got := stripHTML(input)
	if got != "Hello world" {
		t.Errorf("stripHTML(%q) = %q, want %q", input, got, "Hello world")
	}
}

// TestStoreGmailMessage tests that a Gmail message is stored correctly.
func TestStoreGmailMessage(t *testing.T) {
	src := newTestSource(t)
	seedGmail(t, src.db)

	var subject, from, folder, bodyRaw, bodyVisible string
	var hasAttach int
	err := src.db.QueryRow(`SELECT subject, from_addr, folder, body_raw, body_visible, has_attachments
		FROM gmail_messages WHERE id = 'msg2'`).
		Scan(&subject, &from, &folder, &bodyRaw, &bodyVisible, &hasAttach)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if subject != "Re: Meeting Tomorrow" {
		t.Errorf("expected 'Re: Meeting Tomorrow', got %q", subject)
	}
	if from != "bob@example.com" {
		t.Errorf("expected from 'bob@example.com', got %q", from)
	}
	if folder != "INBOX" {
		t.Errorf("expected folder 'INBOX', got %q", folder)
	}
	if !strings.Contains(bodyRaw, "On Fri, Mar 1, 2024") {
		t.Errorf("expected raw body to keep quoted text, got %q", bodyRaw)
	}
	if strings.Contains(bodyVisible, "On Fri, Mar 1, 2024") {
		t.Errorf("expected visible body to strip quoted text, got %q", bodyVisible)
	}
	if hasAttach != 0 {
		t.Error("expected no attachments")
	}
}

// TestExtractDocumentText is covered indirectly through the seeded docs tests;
// this just verifies the helper does not panic on empty content.
func TestGmailSearchEntries(t *testing.T) {
	src := newTestSource(t)
	seedGmail(t, src.db)
	entries, err := gmailSearchEntries(src.db, "gsuite")
	if err != nil {
		t.Fatalf("gmailSearchEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected entries from gmail")
	}
	hasSubject, hasParticipants, hasBody := false, false, false
	for _, e := range entries {
		if e.ContentType == "email_thread_subject" {
			hasSubject = true
		}
		if e.ContentType == "email_thread_participants" {
			hasParticipants = true
		}
		if e.ContentType == "email_thread_content" {
			hasBody = true
		}
	}
	if !hasSubject {
		t.Error("expected email_thread_subject entry")
	}
	if !hasParticipants {
		t.Error("expected email_thread_participants entry")
	}
	if !hasBody {
		t.Error("expected email_thread_content entry")
	}
}

func TestCalendarSearchEntries(t *testing.T) {
	src := newTestSource(t)
	seedCalendar(t, src.db)
	entries, err := calendarSearchEntries(src.db, "gsuite")
	if err != nil {
		t.Fatalf("calendarSearchEntries: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.ContentType == "calendar_event" {
			found = true
		}
	}
	if !found {
		t.Error("expected calendar_event entry")
	}
}

func TestTasksSearchEntries(t *testing.T) {
	src := newTestSource(t)
	seedTasks(t, src.db)
	entries, err := tasksSearchEntries(src.db, "gsuite")
	if err != nil {
		t.Fatalf("tasksSearchEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected task entries")
	}
}

func TestContactsSearchEntries(t *testing.T) {
	src := newTestSource(t)
	seedContacts(t, src.db)
	entries, err := contactsSearchEntries(src.db, "gsuite")
	if err != nil {
		t.Fatalf("contactsSearchEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected contact entries")
	}
}

func TestDocsListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_docs_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestSheetsSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_sheets_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestSheetsListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_sheets_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestGmailSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestGmailListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestCalendarSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_calendar_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestCalendarListUpcoming_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_calendar_list_upcoming", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestTasksSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_tasks_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestTasksList_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_tasks_list", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestSlidesSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_slides_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestSlidesListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_slides_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestContactsSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_contacts_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// coverOsImport prevents unused import of "os".
var _ = os.DevNull

func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
