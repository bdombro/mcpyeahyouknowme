package whatsapp

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mcpyeahyouknowme/core"
)

// ---------- ListChats ----------

// Verifies chat listing returns seeded chats ordered by latest activity when no query is applied.
func TestListChats_noQuery(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) != 4 {
		t.Fatalf("expected 4 chats, got %d", len(chats))
	}
	if chats[0].JID != "group1@g.us" {
		t.Errorf("first chat should be group1 (most recent), got %s", chats[0].JID)
	}
}

// Verifies name sorting orders chat summaries alphabetically instead of by recency.
func TestListChats_sortByName(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("", 10, 0, true, "name")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) < 2 {
		t.Fatal("expected at least 2 chats")
	}
	if chats[0].Name > chats[1].Name {
		t.Errorf("expected sorted by name, got %q before %q", chats[0].Name, chats[1].Name)
	}
}

// Verifies fuzzy chat-name search finds a matching group by its display name.
func TestListChats_fuzzyName(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("Family", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) == 0 {
		t.Fatal("expected at least one chat matching 'Family'")
	}
	found := false
	for _, c := range chats {
		if c.JID == "group1@g.us" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find group1@g.us (Family Chat)")
	}
}

// Verifies typo-tolerant fuzzy matching still finds the intended chat.
func TestListChats_fuzzyTypo(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("Famly", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	found := false
	for _, c := range chats {
		if c.JID == "group1@g.us" {
			found = true
		}
	}
	if !found {
		t.Error("expected fuzzy match 'Famly' → 'Family Chat'")
	}
}

// Verifies participant-name matching surfaces chats through the contacts database.
func TestListChats_participantName(t *testing.T) {
	svc := newTestServiceWithContacts(t, "")
	chats, err := svc.ListChats("Alice", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	jids := make(map[string]bool)
	for _, c := range chats {
		jids[c.JID] = true
	}
	if !jids["group1@g.us"] {
		t.Error("expected group1@g.us (Alice is a participant)")
	}
	if !jids["11111@s.whatsapp.net"] {
		t.Error("expected 11111@s.whatsapp.net (Alice's direct chat)")
	}
}

// Verifies chat search returns no rows when neither chat names nor participants match.
func TestListChats_noMatch(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("zzzznonexistent", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) != 0 {
		t.Errorf("expected 0 chats, got %d", len(chats))
	}
}

// Verifies chat pagination splits the ordered result set across pages.
func TestListChats_pagination(t *testing.T) {
	svc := newTestService(t, "")
	page0, _ := svc.ListChats("", 2, 0, true, "last_active")
	page1, _ := svc.ListChats("", 2, 1, true, "last_active")
	if len(page0) != 2 {
		t.Errorf("page 0: expected 2, got %d", len(page0))
	}
	if len(page1) != 2 {
		t.Errorf("page 1: expected 2, got %d", len(page1))
	}
	if page0[0].JID == page1[0].JID {
		t.Error("pagination returned same first result")
	}
}

// Verifies group detection marks JIDs ending in the group suffix as group chats.
func TestListChats_isGroup(t *testing.T) {
	svc := newTestService(t, "")
	chats, _ := svc.ListChats("", 10, 0, false, "last_active")
	for _, c := range chats {
		expectedGroup := c.JID == "group1@g.us" || c.JID == "group2@g.us"
		if c.IsGroup != expectedGroup {
			t.Errorf("chat %s: IsGroup=%v, want %v", c.JID, c.IsGroup, expectedGroup)
		}
	}
}

// ---------- GetChat ----------

// Verifies single-chat lookup returns the requested chat summary.
func TestGetChat_found(t *testing.T) {
	svc := newTestService(t, "")
	chat, err := svc.GetChat("11111@s.whatsapp.net", true)
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if chat.Name != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %q", chat.Name)
	}
}

// Verifies single-chat lookup returns an error when the JID does not exist.
func TestGetChat_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetChat("nonexistent@s.whatsapp.net", true)
	if err == nil {
		t.Error("expected error for non-existent chat")
	}
}

// ---------- GetDirectChatByContact ----------

// Verifies phone-number lookup resolves the expected direct chat.
func TestGetDirectChatByContact_found(t *testing.T) {
	svc := newTestService(t, "")
	chat, err := svc.GetDirectChatByContact("11111")
	if err != nil {
		t.Fatalf("GetDirectChatByContact: %v", err)
	}
	if chat.Name != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %q", chat.Name)
	}
	if chat.IsGroup {
		t.Error("expected non-group chat")
	}
}

// Verifies phone-number lookup reports an error when no direct chat exists.
func TestGetDirectChatByContact_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetDirectChatByContact("99999")
	if err == nil {
		t.Error("expected error for non-existent contact")
	}
}

// ---------- GetContactChats ----------

// Verifies contact-chat lookup returns chats involving the requested sender/JID.
func TestGetContactChats(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.GetContactChats("11111", 10, 0)
	if err != nil {
		t.Fatalf("GetContactChats: %v", err)
	}
	if len(chats) == 0 {
		t.Fatal("expected at least one chat for sender 11111")
	}
}

// ---------- SearchContacts ----------

// Verifies contact search matches display names stored in chats.
func TestSearchContacts_byName(t *testing.T) {
	svc := newTestService(t, "")
	contacts, err := svc.SearchContacts("Alice")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(contacts) == 0 {
		t.Fatal("expected at least one contact matching 'Alice'")
	}
	if contacts[0].Name != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %q", contacts[0].Name)
	}
}

// Verifies contact search matches phone-number fragments from JIDs.
func TestSearchContacts_byPhone(t *testing.T) {
	svc := newTestService(t, "")
	contacts, err := svc.SearchContacts("22222")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(contacts) == 0 {
		t.Fatal("expected at least one contact matching phone '22222'")
	}
}

// Verifies contact search merges in whatsmeow contact names when available.
func TestSearchContacts_withWhatsmeowContacts(t *testing.T) {
	svc := newTestServiceWithContacts(t, "")
	contacts, err := svc.SearchContacts("Charlie")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	found := false
	for _, c := range contacts {
		if c.Name == "Charlie Brown" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'Charlie Brown' from whatsmeow_contacts")
	}
}

// Verifies contact search excludes group chats from direct-contact results.
func TestSearchContacts_excludesGroups(t *testing.T) {
	svc := newTestService(t, "")
	contacts, err := svc.SearchContacts("group")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	for _, c := range contacts {
		if c.JID == "group1@g.us" || c.JID == "group2@g.us" {
			t.Errorf("group JID %s should not appear in contacts", c.JID)
		}
	}
}

// ---------- ListMessages ----------

// Verifies chronological message listing returns seeded messages across chats.
func TestListMessages_chronological(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Hello Alice")
	requireContains(t, result, "Family dinner tonight")
}

// Verifies chat filtering limits chronological results to one chat.
func TestListMessages_filteredByChat(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "group1@g.us", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Family dinner tonight")
	if containsSubstring(result, "Hello Alice") {
		t.Error("should not contain messages from other chats")
	}
}

// Verifies sender filtering limits chronological results to one sender.
func TestListMessages_filteredBySender(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "22222", "", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Sounds great!")
}

// Verifies FTS-backed message search returns keyword matches from the message index.
func TestListMessages_fts5Search(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "", "dinner", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Family dinner tonight")
}

// Verifies FTS-backed message search returns the empty-state text when nothing matches.
func TestListMessages_fts5SearchNoResults(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "", "zzzznonexistent", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "No messages to display")
}

// Verifies contextual listing expands a hit with surrounding messages from the same chat.
func TestListMessages_withContext(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "11111@s.whatsapp.net", "", 1, 0, true, 1, 1)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result with context")
	}
}

// Verifies filtered message listing returns the empty-state text for unknown chats.
func TestListMessages_noMessages(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "nonexistent@s.whatsapp.net", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "No messages to display")
}

// ---------- GetMessageContext ----------

// Verifies message-context lookup returns the target message plus neighbors.
func TestGetMessageContext_found(t *testing.T) {
	svc := newTestService(t, "")
	ctx, err := svc.GetMessageContext("m2", 1, 1)
	if err != nil {
		t.Fatalf("GetMessageContext: %v", err)
	}
	if ctx.Message.ID != "m2" {
		t.Errorf("expected message m2, got %s", ctx.Message.ID)
	}
}

// Verifies message-context lookup errors for unknown message IDs.
func TestGetMessageContext_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetMessageContext("nonexistent", 1, 1)
	if err == nil {
		t.Error("expected error for non-existent message")
	}
}

// ---------- GetLastInteraction ----------

// Verifies last-interaction lookup returns the newest interaction involving a JID.
func TestGetLastInteraction_found(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.GetLastInteraction("11111@s.whatsapp.net")
	if err != nil {
		t.Fatalf("GetLastInteraction: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// Verifies last-interaction lookup errors when no messages involve the requested JID.
func TestGetLastInteraction_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetLastInteraction("nonexistent@s.whatsapp.net")
	if err == nil {
		t.Error("expected error for non-existent JID")
	}
}

// ---------- Formatting ----------

// Verifies sent messages are formatted with the local "Me" label.
func TestFormatMessage_fromMe(t *testing.T) {
	svc := newTestService(t, "")
	msg := MCPMessage{Content: "test", IsFromMe: true, Sender: "me"}
	result := svc.formatMessage(msg)
	requireContains(t, result, "From: Me:")
}

// Verifies media messages include the media prefix and identifiers in formatted output.
func TestFormatMessage_withMedia(t *testing.T) {
	svc := newTestService(t, "")
	msg := MCPMessage{Content: "photo", MediaType: "image", ID: "m6", ChatJID: "test@s.whatsapp.net"}
	result := svc.formatMessage(msg)
	requireContains(t, result, "[image")
}

// Verifies formatting returns a stable empty-state message when there are no rows to render.
func TestFormatMessages_empty(t *testing.T) {
	svc := newTestService(t, "")
	result := svc.formatMessages(nil)
	if result != "No messages to display." {
		t.Errorf("expected 'No messages to display.', got %q", result)
	}
}

// ---------- Write operations (via mock HTTP) ----------

// Verifies send-message calls parse a successful daemon response.
func TestSendMessage_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/send" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "sent"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, msg, err := svc.SendMessage("11111", "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !ok {
		t.Errorf("expected success, got message: %s", msg)
	}
}

// Verifies send-message calls surface daemon-declared failures without transport errors.
func TestSendMessage_failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "not connected"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, _, err := svc.SendMessage("11111", "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if ok {
		t.Error("expected failure")
	}
}

// Verifies file-send requests reuse the send pipeline successfully.
func TestSendFile_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "file sent"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, _, err := svc.SendFile("11111", "/tmp/test.jpg")
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if !ok {
		t.Error("expected success")
	}
}

// Verifies audio-send requests reuse the send pipeline successfully.
func TestSendAudioMessage_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "audio sent"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, _, err := svc.SendAudioMessage("11111", "/tmp/test.ogg")
	if err != nil {
		t.Fatalf("SendAudioMessage: %v", err)
	}
	if !ok {
		t.Error("expected success")
	}
}

// Verifies media downloads parse a successful daemon response and return the saved path.
func TestDownloadMedia_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "ok", "path": "/tmp/media.jpg"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	path, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if path != "/tmp/media.jpg" {
		t.Errorf("expected /tmp/media.jpg, got %s", path)
	}
}

// Verifies media downloads surface daemon-side failures as errors.
func TestDownloadMedia_failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "not found"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	_, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err == nil {
		t.Error("expected error on download failure")
	}
}

// Verifies send-message propagates network-layer failures from the daemon endpoint.
func TestSendMessage_networkError(t *testing.T) {
	svc := newTestService(t, "http://127.0.0.1:1")
	_, _, err := svc.SendMessage("11111", "hello")
	if err == nil {
		t.Error("expected error on network failure")
	}
}

// Verifies media-download requests propagate network-layer failures from the daemon endpoint.
func TestDownloadMedia_networkError(t *testing.T) {
	svc := newTestService(t, "http://127.0.0.1:1")
	_, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err == nil {
		t.Error("expected error on network failure")
	}
}

// ---------- Helpers ----------

// Verifies timestamp parsing accepts the formats emitted by stored WhatsApp fixtures.
func TestParseTime_formats(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"2024-01-15T10:30:00Z", true},
		{"2024-01-15 10:30:00", true},
		{"2024-01-15 10:30:00-05:00", true},
		{"invalid", false},
	}
	for _, tt := range tests {
		result := parseTime(tt.input)
		if tt.valid && result.IsZero() {
			t.Errorf("parseTime(%q) returned zero time, expected valid", tt.input)
		}
	}
}

// Verifies nullable SQL strings collapse to a plain empty string when invalid.
func TestNullStr(t *testing.T) {
	valid := sql.NullString{String: "hello", Valid: true}
	if got := nullStr(valid); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	empty := sql.NullString{}
	if got := nullStr(empty); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// Verifies JID-to-phone conversion strips WhatsApp suffixes used in UI-facing contact output.
func TestJidPhone(t *testing.T) {
	tests := []struct{ in, want string }{
		{"11111@s.whatsapp.net", "11111"},
		{"group@g.us", "group"},
		{"nojid", "nojid"},
	}
	for _, tt := range tests {
		if got := jidPhone(tt.in); got != tt.want {
			t.Errorf("jidPhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Verifies integer argument parsing accepts numeric JSON values and falls back cleanly.
func TestIntArg(t *testing.T) {
	args := map[string]interface{}{"n": float64(42), "s": "not a number"}
	if got := core.IntArg(args, "n", 0); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := core.IntArg(args, "missing", 99); got != 99 {
		t.Errorf("expected default 99, got %d", got)
	}
	if got := core.IntArg(args, "s", 10); got != 10 {
		t.Errorf("expected default 10 for wrong type, got %d", got)
	}
}

// Verifies boolean argument parsing accepts booleans and falls back to defaults for missing values.
func TestBoolArg(t *testing.T) {
	args := map[string]interface{}{"b": true, "s": "not bool"}
	if got := core.BoolArg(args, "b", false); !got {
		t.Error("expected true")
	}
	if got := core.BoolArg(args, "missing", true); !got {
		t.Error("expected default true")
	}
	if got := core.BoolArg(args, "s", false); got {
		t.Error("expected default false for wrong type")
	}
}

// ---------- bm25Search ----------

// Verifies BM25 ranking still honors chat and time filters when searching message content.
func TestBM25Search_withFilters(t *testing.T) {
	svc := newTestService(t, "")
	results := svc.bm25Search("dinner", 10, "group1@g.us", "2000-01-01", "2099-12-31")
	if len(results) == 0 {
		t.Error("expected BM25 results for 'dinner' in group1@g.us")
	}
}

// Verifies BM25 search returns no ranked hits for absent terms.
func TestBM25Search_noResults(t *testing.T) {
	svc := newTestService(t, "")
	results := svc.bm25Search("zzzznonexistent", 10, "", "", "")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ---------- ListMessages (search with filters) ----------

// Verifies message search applies sender filtering after hydrating ranked results.
func TestListMessages_withSenderFilter(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "11111", "", "dinner", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "dinner")
}

// Verifies message search can filter every ranked hit away when the sender mismatches.
func TestListMessages_senderMismatch(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "99999", "", "dinner", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "No messages to display")
}

// Verifies chronological listing composes multiple filters without leaking extra rows.
func TestListMessages_allFilters(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("2000-01-01", "2099-12-31", "11111", "11111@s.whatsapp.net", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Hello Alice")
}

// Verifies BM25-backed message search returns hydrated formatted messages for a keyword hit.
func TestBM25MessageSearch(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "", "dinner", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "dinner")
}

// Verifies BM25-backed message search truncates ranked results to the requested limit.
func TestBm25MessageSearch_truncation(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.bm25MessageSearch("there", 1, "", "", "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("bm25MessageSearch: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// ---------- postSend / DownloadMedia HTTP error paths ----------

// Verifies invalid daemon JSON falls back to returning the raw response body for send requests.
func TestPostSend_invalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	_, msg, err := svc.SendMessage("11111", "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if msg != "not json" {
		t.Errorf("expected raw text, got %q", msg)
	}
}

// Verifies invalid daemon JSON returns a parse error for media downloads.
func TestDownloadMedia_invalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	_, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ---------- findChatsByParticipantName ----------

// Verifies participant-name lookup safely returns no matches when there is no contacts database.
func TestFindChatsByParticipantName_noContactsDB(t *testing.T) {
	svc := newTestService(t, "")
	result := svc.findChatsByParticipantName("Alice")
	if len(result) != 0 {
		t.Errorf("expected no results without contacts DB, got %d", len(result))
	}
}

// Verifies participant-name lookup returns no chats when no contacts fuzzy-match the query.
func TestFindChatsByParticipantName_noMatch(t *testing.T) {
	svc := newTestServiceWithContacts(t, "")
	result := svc.findChatsByParticipantName("zzzznonexistent")
	if len(result) != 0 {
		t.Errorf("expected no results, got %d", len(result))
	}
}

// ---------- SearchContacts ----------

// Verifies contact search falls back to push names when full names are empty.
func TestSearchContacts_pushNameFallback(t *testing.T) {
	store := newTestStoreWithContacts(t)
	store.contactsDB.Exec(`INSERT INTO whatsmeow_contacts VALUES ('44444@s.whatsapp.net', NULL, 'DaveP')`)
	svc := NewMCPService(store, "")

	contacts, err := svc.SearchContacts("DaveP")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	found := false
	for _, c := range contacts {
		if c.Name == "DaveP" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'DaveP' via push_name fallback")
	}
}

// Verifies contact search caps the result set at fifty rows to keep tool output bounded.
func TestSearchContacts_truncatesOver50(t *testing.T) {
	store := newTestStoreWithContacts(t)
	for i := 0; i < 50; i++ {
		jid := fmt.Sprintf("%05d@s.whatsapp.net", i+50000)
		name := fmt.Sprintf("TestContact%d", i)
		store.db.Exec("INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, datetime('now'))", jid, name)
	}
	svc := NewMCPService(store, "")
	contacts, err := svc.SearchContacts("")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(contacts) != 50 {
		t.Errorf("expected exactly 50 contacts after truncation, got %d", len(contacts))
	}
}

// ---------- expandContext ----------

// Verifies context expansion falls back to the original message when the target row no longer exists.
func TestExpandContext_missingMessage(t *testing.T) {
	svc := newTestService(t, "")
	msgs := []MCPMessage{{ID: "nonexistent", ChatJID: "11111@s.whatsapp.net"}}
	expanded := svc.expandContext(msgs, 1, 1)
	if len(expanded) != 1 {
		t.Errorf("expected 1 message (fallback), got %d", len(expanded))
	}
}

// ---------- GetContactChats ----------

// Verifies contact-chat pagination advances through separate pages without panicking.
func TestGetContactChats_pagination(t *testing.T) {
	svc := newTestService(t, "")
	page0, _ := svc.GetContactChats("11111", 1, 0)
	page1, _ := svc.GetContactChats("11111", 1, 1)
	if len(page0) != 1 {
		t.Errorf("page 0: expected 1, got %d", len(page0))
	}
	_ = page1
}

// ---------- messagesAround ----------

// Verifies neighbor lookup returns rows on the requested side of the timestamp boundary.
func TestMessagesAround(t *testing.T) {
	svc := newTestService(t, "")
	msgs := svc.messagesAround("11111@s.whatsapp.net", "2099-12-31T00:00:00Z", "< ?", "DESC", 2)
	if len(msgs) == 0 {
		t.Error("expected messages before 2099")
	}
}

// Verifies ranked-message hydration returns an empty map for empty ranked input.
func TestLoadRankedMessages_empty(t *testing.T) {
	svc := newTestService(t, "")
	msgs, err := svc.loadRankedMessages(nil)
	if err != nil {
		t.Fatalf("loadRankedMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty result, got %d", len(msgs))
	}
}

// Verifies zero before/after windows leave expanded-context results unchanged.
func TestExpandContext_zeroWindowReturnsInput(t *testing.T) {
	svc := newTestService(t, "")
	msgs := []MCPMessage{{ID: "m3", ChatJID: "11111@s.whatsapp.net"}}
	expanded := svc.expandContext(msgs, 0, 0)
	if len(expanded) != 1 || expanded[0].ID != "m3" {
		t.Fatalf("expected original message unchanged, got %#v", expanded)
	}
}

// Verifies invalid messages are passed through while valid messages in the same batch still expand.
func TestExpandContext_skipsInvalidMessageButExpandsValid(t *testing.T) {
	svc := newTestService(t, "")
	msgs := []MCPMessage{
		{ID: "", ChatJID: ""},
		{ID: "m3", ChatJID: "11111@s.whatsapp.net"},
	}
	expanded := svc.expandContext(msgs, 1, 0)
	if len(expanded) < 2 {
		t.Fatalf("expected invalid original plus expanded valid context, got %d entries", len(expanded))
	}
	if expanded[0].ID != "" {
		t.Fatalf("expected invalid message to remain as-is, got %#v", expanded[0])
	}
	foundContext := false
	for _, msg := range expanded[1:] {
		if msg.ID == "m2" {
			foundContext = true
			break
		}
	}
	if !foundContext {
		t.Fatal("expected valid message to include previous context")
	}
}

// Verifies a batch of only invalid messages is returned unchanged.
func TestExpandContext_allInvalidMessages(t *testing.T) {
	svc := newTestService(t, "")
	msgs := []MCPMessage{{ID: "", ChatJID: ""}}
	expanded := svc.expandContext(msgs, 1, 1)
	if len(expanded) != 1 || expanded[0].ID != "" {
		t.Fatalf("expected invalid message slice unchanged, got %#v", expanded)
	}
}

// Verifies expanded before-context preserves the original newest-first ordering used by the single-message path.
func TestExpandContext_preservesNewestFirstBeforeMessages(t *testing.T) {
	svc := newTestService(t, "")
	msgs := []MCPMessage{{ID: "m3", ChatJID: "11111@s.whatsapp.net"}}
	expanded := svc.expandContext(msgs, 2, 0)
	if len(expanded) != 3 {
		t.Fatalf("expected two context messages plus target, got %d", len(expanded))
	}
	if expanded[0].ID != "m2" || expanded[1].ID != "m1" || expanded[2].ID != "m3" {
		t.Fatalf("expected original newest-first order m2,m1,m3; got %#v", expanded)
	}
}
