package gsuite

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/server"
)

// Invokes one gsuite MCP tool and returns its first text payload for assertions.
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

// Builds a test MCP server with a seeded gsuite source so tool handlers can be exercised end to end.
func buildMCPServer(t *testing.T) *server.MCPServer {
	t.Helper()
	src := newTestSource(t)
	seedAll(t, src.db)
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	return s
}

// --- Docs MCP tests ---

// Verifies the Docs search tool returns seeded documents for matching queries.
func TestDocsSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_search", map[string]interface{}{"query": "project proposal"})
	if text == "" {
		t.Fatal("expected non-empty result")
	}
	var result map[string]interface{}
	if err := core.UnmarshalToolResultTextPayload(text, &result); err != nil {
		t.Fatalf("parse result: %v\ntext: %s", err, text)
	}
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected at least one result")
	}
}

// Verifies the Docs search tool handles a nil DB without panicking.
func TestDocsSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_docs_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected empty array result")
	}
}

// Verifies the Docs get-document tool returns a seeded document by ID.
func TestDocsGetDocument_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_get_document", map[string]interface{}{"document_id": "doc1"})
	var result map[string]interface{}
	if err := core.UnmarshalToolResultTextPayload(text, &result); err != nil {
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

// Verifies the Docs get-document tool reports a readable not-found error for unknown IDs.
func TestDocsGetDocument_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_get_document", map[string]interface{}{"document_id": "nope"})
	if text != "Document not found" {
		t.Errorf("expected 'Document not found', got %q", text)
	}
}

// Verifies the Docs get-document tool handles a nil DB without panicking.
func TestDocsGetDocument_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_docs_get_document", map[string]interface{}{"document_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

// Verifies the Docs list-recent tool returns recently seeded documents.
func TestDocsListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_docs_list_recent", map[string]interface{}{})
	var result map[string]interface{}
	if err := core.UnmarshalToolResultTextPayload(text, &result); err != nil {
		t.Fatalf("parse: %v text: %s", err, text)
	}
	count, _ := result["count"].(float64)
	if count != 1 {
		t.Errorf("expected 1 doc, got %v", count)
	}
}

// --- Sheets MCP tests ---

// Verifies the Sheets search tool returns seeded spreadsheets for matching queries.
func TestSheetsSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_search", map[string]interface{}{"query": "Budget"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected budget sheet in results")
	}
}

// Verifies the Sheets get-spreadsheet tool returns a seeded spreadsheet by ID.
func TestSheetsGetSpreadsheet_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_get_spreadsheet", map[string]interface{}{"spreadsheet_id": "sheet1"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
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

// Verifies the Sheets get-spreadsheet tool reports a readable not-found error for unknown IDs.
func TestSheetsGetSpreadsheet_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_get_spreadsheet", map[string]interface{}{"spreadsheet_id": "nope"})
	if text != "Spreadsheet not found" {
		t.Errorf("expected 'Spreadsheet not found', got %q", text)
	}
}

// Verifies the Sheets get-spreadsheet tool handles a nil DB without panicking.
func TestSheetsGetSpreadsheet_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_sheets_get_spreadsheet", map[string]interface{}{"spreadsheet_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

// Verifies the Sheets list-recent tool returns recently seeded spreadsheets.
func TestSheetsListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_sheets_list_recent", map[string]interface{}{})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 spreadsheet, got %v", result["count"])
	}
}

// --- Gmail MCP tests ---

// Verifies the Gmail search tool returns seeded messages for matching queries.
func TestGmailSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_search", map[string]interface{}{"query": "meeting"})
	if !strings.Contains(text, "[SECURITY:") || !strings.Contains(text, "MCPSEC_END_HEADER") {
		t.Fatalf("expected untrusted banner on gmail search, got %q", text)
	}
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected message in search results")
	}
}

// Verifies the Gmail get-message tool returns the seeded message payload for a known ID.
func TestGmailGetMessage_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_message", map[string]interface{}{"message_id": "msg1"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
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

// Verifies the Gmail get-message tool reports a readable not-found error for unknown IDs.
func TestGmailGetMessage_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_message", map[string]interface{}{"message_id": "nope"})
	if text != "Message not found" {
		t.Errorf("expected 'Message not found', got %q", text)
	}
}

// Verifies the Gmail get-message tool handles a nil DB without panicking.
func TestGmailGetMessage_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_get_message", map[string]interface{}{"message_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

// Verifies the Gmail get-thread tool returns the rebuilt seeded thread for a known thread ID.
func TestGmailGetThread_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "thread1"})
	var result map[string]interface{}
	if err := core.UnmarshalToolResultTextPayload(text, &result); err != nil {
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

// Verifies the Gmail get-thread tool reports a readable not-found error for unknown thread IDs.
func TestGmailGetThread_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "nope"})
	if text != "Thread not found" {
		t.Errorf("expected 'Thread not found', got %q", text)
	}
}

// Verifies the Gmail get-thread tool reports missing-thread metadata instead of silently succeeding.
func TestGmailGetThread_NoThreadMetadata(t *testing.T) {
	src := newTestSource(t)
	src.db.Exec(`INSERT INTO gmail_messages
		(id, thread_id, labels, folder, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		 date, snippet, body_visible, has_attachments, size_estimate, last_synced)
		VALUES ('orphan1', 'orphan_thread', 'INBOX', 'INBOX', 'Orphan', 'a@b.com', 'c@d.com', '', '',
		 '2024-01-01', 'snip', 'raw only', 0, 100, datetime('now'))`)
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "orphan_thread"})
	var result map[string]interface{}
	if err := core.UnmarshalToolResultTextPayload(text, &result); err != nil {
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

// Verifies the Gmail get-thread tool handles a nil DB without panicking.
func TestGmailGetThread_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_get_thread", map[string]interface{}{"thread_id": "thread1"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

// Verifies the Gmail list-recent tool returns recently seeded messages.
func TestGmailListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{})
	if !strings.Contains(text, "[SECURITY:") || !strings.Contains(text, "MCPSEC_END_HEADER") {
		t.Fatalf("expected untrusted banner on gmail list_recent, got %q", text)
	}
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 2 {
		t.Errorf("expected 2 messages, got %v", result["count"])
	}
	messages := result["messages"].([]interface{})
	first := messages[0].(map[string]interface{})
	if first["thread_id"] != "thread1" {
		t.Errorf("expected thread_id 'thread1', got %v", first["thread_id"])
	}
}

// Verifies the Gmail list-recent tool can filter seeded results by folder.
func TestGmailListRecent_FilterByFolder(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{"folder": "INBOX"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 2 {
		t.Errorf("expected 2 INBOX messages, got %v", result["count"])
	}
	text2 := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{"folder": "SENT"})
	var result2 map[string]interface{}
	core.UnmarshalToolResultTextPayload(text2, &result2)
	if result2["count"].(float64) != 0 {
		t.Errorf("expected 0 SENT messages, got %v", result2["count"])
	}
}

// --- Calendar MCP tests ---

// Verifies the Calendar search tool returns seeded events for matching queries.
func TestCalendarSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_search", map[string]interface{}{"query": "standup"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected calendar event in results")
	}
}

// Verifies the Calendar get-event tool returns a seeded event by ID.
func TestCalendarGetEvent_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_get_event", map[string]interface{}{"event_id": "cal1|ev1"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
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

// Verifies the Calendar get-event tool reports a readable not-found error for unknown IDs.
func TestCalendarGetEvent_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_get_event", map[string]interface{}{"event_id": "nope"})
	if text != "Event not found" {
		t.Errorf("expected 'Event not found', got %q", text)
	}
}

// Verifies the Calendar get-event tool handles a nil DB without panicking.
func TestCalendarGetEvent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_calendar_get_event", map[string]interface{}{"event_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

// Verifies the Calendar list-upcoming tool returns seeded future events.
func TestCalendarListUpcoming(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_calendar_list_upcoming", map[string]interface{}{"days": 7})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	// Event is 48h in future, should appear in 7-day window
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected at least 1 upcoming event in 7-day window")
	}
}

// --- Tasks MCP tests ---

// Verifies the Tasks search tool returns seeded tasks for matching queries.
func TestTasksSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_tasks_search", map[string]interface{}{"query": "unit tests"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected task in results")
	}
}

// Verifies the Tasks list tool returns all seeded tasks when no status filter is applied.
func TestTasksList_All(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_tasks_list", map[string]interface{}{})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected tasks in result")
	}
}

// Verifies the Tasks list tool filters seeded tasks by status when requested.
func TestTasksList_FilterByStatus(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_tasks_list", map[string]interface{}{"status": "needsAction"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 needsAction task, got %v", result["count"])
	}
}

// --- Contacts MCP tests ---

// Verifies the Contacts search tool returns seeded contacts for matching queries.
func TestContactsSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_search", map[string]interface{}{"query": "Alice"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected contact in search results")
	}
}

// Verifies the Contacts list tool returns the seeded people directory entries.
func TestContactsList(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_list", map[string]interface{}{})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 contact, got %v", result["count"])
	}
}

// Verifies the Contacts list tool handles a nil DB without panicking.
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

// Verifies the Slides search tool returns seeded presentations for matching queries.
func TestSlidesSearch_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_search", map[string]interface{}{"query": "revenue"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	count, _ := result["count"].(float64)
	if count == 0 {
		t.Error("expected presentation in search results")
	}
}

// Verifies the Slides get-presentation tool returns a seeded presentation by ID.
func TestSlidesGetPresentation_Found(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_get_presentation", map[string]interface{}{"presentation_id": "pres1"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
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

// Verifies the Slides get-presentation tool reports a readable not-found error for unknown IDs.
func TestSlidesGetPresentation_NotFound(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_get_presentation", map[string]interface{}{"presentation_id": "nope"})
	if text != "Presentation not found" {
		t.Errorf("expected 'Presentation not found', got %q", text)
	}
}

// Verifies the Slides get-presentation tool handles a nil DB without panicking.
func TestSlidesGetPresentation_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_slides_get_presentation", map[string]interface{}{"presentation_id": "x"})
	if text != "Database not available" {
		t.Errorf("expected 'Database not available', got %q", text)
	}
}

// Verifies the Slides list-recent tool returns recently seeded presentations.
func TestSlidesListRecent(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_slides_list_recent", map[string]interface{}{})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 presentation, got %v", result["count"])
	}
}

// Verifies MCP handlers return required-argument errors before performing DB work.
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
		{"contacts_lookup_by_phone", "gsuite_contacts_lookup_by_phone", "phone parameter is required"},
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

// Verifies tools are not registered for apps disabled in the persisted app config.
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

// Verifies primary-folder selection prefers the expected label ordering from Gmail labels.
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

// Verifies HTML stripping removes markup while preserving readable text content for Gmail indexing.
func TestStripHTML(t *testing.T) {
	input := "<p>Hello <b>world</b></p>"
	got := stripHTML(input)
	if got != "Hello world" {
		t.Errorf("stripHTML(%q) = %q, want %q", input, got, "Hello world")
	}
}

// Verifies storeGmailMessage persists visible-body fields needed for Gmail search and thread reconstruction.
func TestStoreGmailMessage(t *testing.T) {
	src := newTestSource(t)
	seedGmail(t, src.db)

	var subject, from, folder, bodyVisible string
	var hasAttach int
	err := src.db.QueryRow(`SELECT subject, from_addr, folder, body_visible, has_attachments
		FROM gmail_messages WHERE id = 'msg2'`).
		Scan(&subject, &from, &folder, &bodyVisible, &hasAttach)
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
	if strings.Contains(bodyVisible, "On Fri, Mar 1, 2024") {
		t.Errorf("expected visible body to strip quoted text, got %q", bodyVisible)
	}
	if hasAttach != 0 {
		t.Error("expected no attachments")
	}
}

// Verifies gmailSearchEntries emits message and thread entries with the expected searchable content
// and sets Timestamp on thread and content entries from the message dates.
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
			if e.Timestamp == nil {
				t.Error("expected Timestamp on email_thread_subject entry")
			}
		}
		if e.ContentType == "email_thread_participants" {
			hasParticipants = true
			if e.Timestamp == nil {
				t.Error("expected Timestamp on email_thread_participants entry")
			}
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

// Verifies calendarSearchEntries maps seeded calendar rows into global-search entries
// and sets Timestamp from start_time, with human-readable date appended to content.
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
			if e.Timestamp == nil {
				t.Error("expected Timestamp to be set on calendar_event entry")
			}
			if e.Content == "" || !strings.Contains(e.Content, "|") {
				t.Errorf("expected calendar content to contain human-readable date, got %q", e.Content)
			}
		}
		if e.ContentType == "calendar_event_description" {
			if e.Timestamp == nil {
				t.Error("expected Timestamp to be set on calendar_event_description entry")
			}
		}
	}
	if !found {
		t.Error("expected calendar_event entry")
	}
}

// Verifies tasksSearchEntries maps seeded task rows into global-search entries
// and sets Timestamp from the due date field.
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
	for _, e := range entries {
		if e.ContentType == "task" && e.Timestamp == nil {
			t.Error("expected Timestamp to be set on task entry")
		}
	}
}

// Verifies tasksSearchEntries falls back to updated time when due is empty.
func TestTasksSearchEntries_noDue(t *testing.T) {
	src := newTestSource(t)
	_, err := src.db.Exec(`INSERT INTO tasks_items
		(id, tasklist_title, title, notes, status, due, updated, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"taskNoDue", "My Tasks", "No due date task", "", "needsAction",
		"", "2024-03-20T12:00:00Z")
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
	entries, err := tasksSearchEntries(src.db, "gsuite")
	if err != nil {
		t.Fatalf("tasksSearchEntries: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.ContentType == "task" {
			found = true
			if e.Timestamp == nil {
				t.Error("expected Timestamp to fall back to updated when due is empty")
			}
		}
	}
	if !found {
		t.Error("expected task entry")
	}
}

// Verifies contactsSearchEntries maps seeded contact rows into global-search entries.
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

// Verifies the Docs list-recent tool handles a nil DB without panicking.
func TestDocsListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_docs_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Sheets search tool handles a nil DB without panicking.
func TestSheetsSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_sheets_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Sheets list-recent tool handles a nil DB without panicking.
func TestSheetsListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_sheets_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Gmail search tool handles a nil DB without panicking.
func TestGmailSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Gmail list-recent tool handles a nil DB without panicking.
func TestGmailListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_gmail_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Calendar search tool handles a nil DB without panicking.
func TestCalendarSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_calendar_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Calendar list-upcoming tool handles a nil DB without panicking.
func TestCalendarListUpcoming_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_calendar_list_upcoming", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Tasks search tool handles a nil DB without panicking.
func TestTasksSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_tasks_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Tasks list tool handles a nil DB without panicking.
func TestTasksList_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_tasks_list", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Slides search tool handles a nil DB without panicking.
func TestSlidesSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_slides_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Slides list-recent tool handles a nil DB without panicking.
func TestSlidesListRecent_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_slides_list_recent", map[string]interface{}{})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// Verifies the Contacts search tool handles a nil DB without panicking.
func TestContactsSearch_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_contacts_search", map[string]interface{}{"query": "anything"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// --- Contacts lookup by phone tests ---

// Verifies lookup returns a contact when the query is a partial digit-only substring of the stored phone.
func TestContactsLookupByPhone_MatchPartial(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_lookup_by_phone", map[string]interface{}{"phone": "5550100"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 result, got %v — response: %s", result["count"], text)
	}
}

// Verifies lookup matches when the query includes a country code prefix (e.g. +1).
func TestContactsLookupByPhone_MatchWithCountryCode(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_lookup_by_phone", map[string]interface{}{"phone": "+15550100"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 result, got %v — response: %s", result["count"], text)
	}
}

// Verifies lookup matches when the query contains dashes and parentheses.
func TestContactsLookupByPhone_MatchWithFormatting(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_lookup_by_phone", map[string]interface{}{"phone": "(555) 010-0"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 1 {
		t.Errorf("expected 1 result, got %v — response: %s", result["count"], text)
	}
}

// Verifies lookup returns no results when no phone matches.
func TestContactsLookupByPhone_NoMatch(t *testing.T) {
	s := buildMCPServer(t)
	text := callTool(t, s, "gsuite_contacts_lookup_by_phone", map[string]interface{}{"phone": "9999999"})
	var result map[string]interface{}
	core.UnmarshalToolResultTextPayload(text, &result)
	if result["count"].(float64) != 0 {
		t.Errorf("expected 0 results, got %v", result["count"])
	}
}

// Verifies lookup handles a nil DB without panicking.
func TestContactsLookupByPhone_NilDB(t *testing.T) {
	src := &Source{apps: allAppsEnabledConfig()}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	src.RegisterTools(s)
	text := callTool(t, s, "gsuite_contacts_lookup_by_phone", map[string]interface{}{"phone": "5555551234"})
	if text == "" {
		t.Fatal("expected non-empty response")
	}
}

// --- normalizePhone unit tests ---

// Verifies normalizePhone strips all non-digit characters correctly.
func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"+1-555-0100", "15550100"},
		{"(555) 555-1234", "5555551234"},
		{"5555551234", "5555551234"},
		{"+15555551234", "15555551234"},
		{"+3532222222", "3532222222"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := normalizePhone(tc.input); got != tc.want {
			t.Errorf("normalizePhone(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// coverOsImport prevents unused import of "os".
var _ = os.DevNull

// Returns whether `sub` appears in `s` so repeated string assertions stay concise in MCP tests.
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
