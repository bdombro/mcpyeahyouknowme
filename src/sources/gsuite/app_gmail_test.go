package gsuite

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/api/gmail/v1"
)

// Encodes fixture strings as base64url payloads so Gmail part bodies can be built inline in tests.
func b64(s string) string {
	return base64.URLEncoding.EncodeToString([]byte(s))
}

// Verifies body-field merging preserves the visible/raw/body-text fields used by Gmail storage migrations.
func TestMergeGmailMessageBodyFields(t *testing.T) {
	nextText, nextRaw, nextVis, changed := mergeGmailMessageBodyFields("hello", "", "")
	if !changed || nextRaw != "hello" || nextVis != "hello" || nextText != "hello" {
		t.Fatalf("expected fill from body_text, got text=%q raw=%q vis=%q changed=%v", nextText, nextRaw, nextVis, changed)
	}
	_, _, _, same := mergeGmailMessageBodyFields("a", "a", "a")
	if same {
		t.Fatal("expected no update when all fields already match")
	}
}

// Verifies Gmail message storage records map headers and body fields into the persisted message shape.
func TestBuildGmailStoredRecord(t *testing.T) {
	rec := buildGmailStoredRecord(&gmail.Message{
		Id:           "m1",
		ThreadId:     "t1",
		LabelIds:     []string{"INBOX", "UNREAD"},
		Snippet:      "snip",
		SizeEstimate: 99,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/alternative",
			Parts: []*gmail.MessagePart{
				{
					MimeType: "text/plain",
					Body:     &gmail.MessagePartBody{Data: b64("Hi")},
				},
			},
			Headers: []*gmail.MessagePartHeader{
				{Name: "Subject", Value: "Hello"},
				{Name: "From", Value: "a@b.com"},
				{Name: "To", Value: "c@d.com"},
				{Name: "Date", Value: "Mon, 1 Apr 2024 10:00:00 +0000"},
			},
		},
	})
	if rec.ID != "m1" || rec.ThreadID != "t1" || rec.Folder != "INBOX" {
		t.Fatalf("unexpected record: %#v", rec)
	}
	if rec.Subject != "Hello" || rec.BodyRaw != "Hi" || rec.BodyVisible != "Hi" {
		t.Fatalf("unexpected headers/body: %#v", rec)
	}
	if rec.HasAttachments != 0 {
		t.Fatalf("expected no attachments, got %#v", rec)
	}
}

// Verifies Gmail body extraction prefers plain text parts and falls back to HTML when needed.
func TestExtractGmailBody_plainInPartAndHTMLFallback(t *testing.T) {
	got := extractGmailBody(&gmail.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmail.MessagePart{
			{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: b64("plain body")}},
		},
	})
	if got != "plain body" {
		t.Fatalf("expected plain from nested part, got %q", got)
	}
	htmlGot := extractGmailBody(&gmail.MessagePart{
		MimeType: "text/html",
		Body:     &gmail.MessagePartBody{Data: b64("<p>x<b>y</b></p>")},
	})
	if htmlGot != "xy" {
		t.Fatalf("expected stripped html, got %q", htmlGot)
	}
	if extractGmailBody(&gmail.MessagePart{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: "!!!"}}) != "" {
		t.Fatal("expected invalid base64 to yield empty string")
	}
}

// Verifies attachment detection walks nested MIME parts instead of only checking the top level.
func TestHasGmailAttachments_nested(t *testing.T) {
	if hasGmailAttachments(nil) {
		t.Fatal("nil payload should have no attachments")
	}
	tree := &gmail.MessagePart{
		Parts: []*gmail.MessagePart{
			{
				Parts: []*gmail.MessagePart{
					{
						Filename: "a.pdf",
						Body:     &gmail.MessagePartBody{AttachmentId: "att1"},
					},
				},
			},
		},
	}
	if !hasGmailAttachments(tree) {
		t.Fatal("expected nested attachment")
	}
}

// Verifies Gmail header parsing extracts the expected standard header values from message payloads.
func TestParseGmailHeaders(t *testing.T) {
	h := parseGmailHeaders(&gmail.Message{
		Payload: &gmail.MessagePart{
			Headers: []*gmail.MessagePartHeader{
				{Name: "Subject", Value: "S"},
				{Name: "X-Other", Value: "ignore"},
				{Name: "From", Value: "f@e.com"},
			},
		},
	})
	if h["Subject"] != "S" || h["From"] != "f@e.com" || h["X-Other"] != "" {
		t.Fatalf("unexpected headers map: %#v", h)
	}
	if len(parseGmailHeaders(&gmail.Message{})) != 0 {
		t.Fatal("expected empty map for nil payload")
	}
}

// Verifies transcript-entry formatting emits placeholders when visible body content is missing.
func TestFormatThreadTranscriptEntry_placeholders(t *testing.T) {
	s := formatThreadTranscriptEntry(gmailMessageRecord{BodyVisible: "x"})
	if !strings.Contains(s, "unknown date") || !strings.Contains(s, "unknown sender") {
		t.Fatalf("expected placeholders, got %q", s)
	}
}

// Verifies thread search-entry building emits searchable chunks for a stored Gmail thread.
func TestGmailSearchEntriesForThread(t *testing.T) {
	msgs := []gmailMessageRecord{
		{ID: "a", ThreadID: "t1", Subject: "Subj", From: "a@b.com", BodyVisible: "one"},
		{ID: "b", ThreadID: "t1", From: "c@d.com", BodyVisible: "two"},
	}
	entries := gmailSearchEntriesForThread("gsuite", gmailThreadSearchSummary{
		threadID:     "t1",
		subject:      "Subj",
		participants: "a@b.com, c@d.com",
		messageCount: 2,
		firstDate:    "2024-01-01",
		lastDate:     "2024-01-02",
	}, msgs)
	var types []string
	for _, e := range entries {
		types = append(types, e.ContentType)
	}
	if len(types) < 3 {
		t.Fatalf("expected subject + participants + at least one chunk, got %#v", types)
	}
	if types[0] != "email_thread_subject" || types[1] != "email_thread_participants" {
		t.Fatalf("unexpected entry order/types: %#v", types)
	}
	foundContent := false
	for _, e := range entries {
		if e.ContentType == "email_thread_content" {
			foundContent = true
			if !strings.Contains(e.Content, "one") {
				t.Fatalf("expected chunk to include message body, got %q", e.Content)
			}
		}
	}
	if !foundContent {
		t.Fatal("expected email_thread_content entry")
	}
	// Metadata should parse as JSON
	var meta map[string]interface{}
	if err := json.Unmarshal(entries[0].Metadata, &meta); err != nil {
		t.Fatalf("metadata json: %v", err)
	}
}

// Verifies visible-body derivation strips quoted reply blocks from authored Gmail content.
func TestDeriveVisibleBody_stripsQuotedReply(t *testing.T) {
	raw := "Yes, that works for me.\n\nOn Fri, Mar 1, 2024 at 10:00 AM Alice <alice@example.com> wrote:\n> Can you make the meeting tomorrow?"
	got := deriveVisibleBody(raw)
	if strings.Contains(got, "On Fri, Mar 1, 2024") {
		t.Fatalf("expected quoted reply to be stripped, got %q", got)
	}
	if got != "Yes, that works for me." {
		t.Fatalf("expected visible body to keep authored text, got %q", got)
	}
}

// Verifies visible-body derivation falls back to raw content when aggressive stripping would remove everything useful.
func TestDeriveVisibleBody_fallsBackToRawWhenOnlyQuotedTextRemains(t *testing.T) {
	raw := "> Prior quoted line\n> Another quoted line"
	got := deriveVisibleBody(raw)
	if got != raw {
		t.Fatalf("expected fallback to raw quoted body, got %q", got)
	}
}

// Verifies thread-chunk building splits long transcripts into multiple search entries.
func TestBuildGmailThreadChunks_splitsLongThreadContent(t *testing.T) {
	longBody := strings.Repeat("chunked content ", 260)
	messages := []gmailMessageRecord{
		{
			ID:          "msg1",
			ThreadID:    "thread1",
			Subject:     "Quarterly Planning",
			From:        "alice@example.com",
			Date:        "2024-03-01T10:00:00Z",
			BodyVisible: longBody,
		},
		{
			ID:          "msg2",
			ThreadID:    "thread1",
			Subject:     "Re: Quarterly Planning",
			From:        "bob@example.com",
			Date:        "2024-03-01T11:00:00Z",
			BodyVisible: "Replying with final confirmation.",
		},
	}

	chunks := buildGmailThreadChunks("Quarterly Planning", "alice@example.com, bob@example.com", messages)
	if len(chunks) < 2 {
		t.Fatalf("expected long thread to split into multiple chunks, got %d", len(chunks))
	}
	if chunks[0].StartMessageID != "msg1" {
		t.Fatalf("expected first chunk to start with msg1, got %#v", chunks[0])
	}
	last := chunks[len(chunks)-1]
	if last.EndMessageID != "msg2" {
		t.Fatalf("expected last chunk to end with msg2, got %#v", last)
	}
}

// Verifies visible-body derivation strips quoted header blocks and trailing mobile signatures.
func TestDeriveVisibleBody_stripsHeaderBlockAndMobileSignature(t *testing.T) {
	raw := "Looks good to me.\n\nSent from my iPhone\n\nFrom: Alice <alice@example.com>\nSent: Friday, March 1, 2024 10:00 AM\nTo: Bob <bob@example.com>\nSubject: Meeting Tomorrow"
	got := deriveVisibleBody(raw)
	if strings.Contains(got, "Sent from my iPhone") {
		t.Fatalf("expected mobile signature to be stripped, got %q", got)
	}
	if strings.Contains(got, "From: Alice") {
		t.Fatalf("expected quoted header block to be stripped, got %q", got)
	}
	if got != "Looks good to me." {
		t.Fatalf("expected authored text to remain, got %q", got)
	}
}

// Verifies long transcript entries split on newline boundaries when possible.
func TestSplitLongThreadEntry_splitsOnNewlineBoundary(t *testing.T) {
	entry := "[2024-03-01T10:00:00Z] alice@example.com\nfirst paragraph\nsecond paragraph\nthird paragraph"
	parts := splitLongThreadEntry(entry, 50)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts, got %#v", parts)
	}
	for _, part := range parts {
		if part == "" {
			t.Fatalf("expected non-empty part, got %#v", parts)
		}
	}
}

// Verifies Gmail schema initialization backfills legacy body columns and rebuilds thread rows.
func TestInitGmailSchema_backfillsLegacyRowsAndBuildsThreads(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:?_fk=on&cache=shared")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE gmail_messages (
		id TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL DEFAULT '',
		labels TEXT NOT NULL DEFAULT '',
		folder TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL DEFAULT '',
		from_addr TEXT NOT NULL DEFAULT '',
		to_addrs TEXT NOT NULL DEFAULT '',
		cc_addrs TEXT NOT NULL DEFAULT '',
		bcc_addrs TEXT NOT NULL DEFAULT '',
		date TEXT NOT NULL DEFAULT '',
		snippet TEXT NOT NULL DEFAULT '',
		body_text TEXT NOT NULL DEFAULT '',
		has_attachments INTEGER NOT NULL DEFAULT 0,
		size_estimate INTEGER NOT NULL DEFAULT 0,
		last_synced TEXT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	legacyBody := "Yes, I can make the meeting tomorrow and I will bring the revised deck.\n\nOn Fri, Mar 1, 2024 at 10:00 AM Alice <alice@example.com> wrote:\n> Can you make the meeting tomorrow?"
	_, err = db.Exec(`INSERT INTO gmail_messages (
		id, thread_id, labels, folder, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		date, snippet, body_text, has_attachments, size_estimate, last_synced
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"legacy1", "thread1", "INBOX", "INBOX", "Meeting Tomorrow", "alice@example.com", "bob@example.com", "", "",
		"Fri, 01 Mar 2024 10:00:00 +0000", "Can you make the meeting tomorrow?", legacyBody, 0, 1234)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := initGmailSchema(db); err != nil {
		t.Fatalf("initGmailSchema: %v", err)
	}

	hasBodyRaw, err := tableHasColumn(db, "gmail_messages", "body_raw")
	if err != nil {
		t.Fatalf("tableHasColumn body_raw: %v", err)
	}
	if !hasBodyRaw {
		t.Fatal("expected body_raw column to exist after migration")
	}
	hasBodyVisible, err := tableHasColumn(db, "gmail_messages", "body_visible")
	if err != nil {
		t.Fatalf("tableHasColumn body_visible: %v", err)
	}
	if !hasBodyVisible {
		t.Fatal("expected body_visible column to exist after migration")
	}

	var bodyRaw, bodyVisible string
	if err := db.QueryRow(`SELECT body_raw, body_visible FROM gmail_messages WHERE id = 'legacy1'`).Scan(&bodyRaw, &bodyVisible); err != nil {
		t.Fatalf("query migrated bodies: %v", err)
	}
	if !strings.Contains(bodyRaw, "On Fri, Mar 1, 2024") {
		t.Fatalf("expected body_raw to preserve quoted text, got %q", bodyRaw)
	}
	if strings.Contains(bodyVisible, "On Fri, Mar 1, 2024") {
		t.Fatalf("expected body_visible to strip quoted text, got %q", bodyVisible)
	}

	meta, err := loadGmailThreadMeta(db, "thread1")
	if err != nil {
		t.Fatalf("loadGmailThreadMeta: %v", err)
	}
	if meta.MessageCount != 1 {
		t.Fatalf("expected derived thread row, got %#v", meta)
	}
	if meta.threadID != "thread1" {
		t.Fatalf("expected thread1 metadata, got %#v", meta)
	}
}

// Verifies direct message storage persists Gmail rows without needing the higher-level sync path.
func TestStoreGmailMessage_direct(t *testing.T) {
	db := newTestDB(t)
	storeGmailMessage(db, &gmail.Message{
		Id:       "direct1",
		ThreadId: "t99",
		LabelIds: []string{"INBOX"},
		Snippet:  "hello world",
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Body:     &gmail.MessagePartBody{Data: b64("Hello from direct store")},
			Headers: []*gmail.MessagePartHeader{
				{Name: "Subject", Value: "Direct"},
				{Name: "From", Value: "x@y.com"},
				{Name: "Date", Value: "Mon, 1 Apr 2024 10:00:00 +0000"},
			},
		},
	})
	var subject, bodyRaw string
	err := db.QueryRow(`SELECT subject, body_raw FROM gmail_messages WHERE id = 'direct1'`).Scan(&subject, &bodyRaw)
	if err != nil {
		t.Fatalf("query stored row: %v", err)
	}
	if subject != "Direct" || bodyRaw != "Hello from direct store" {
		t.Fatalf("unexpected stored values: subject=%q bodyRaw=%q", subject, bodyRaw)
	}
}

// Verifies Gmail record building returns nil for nil Gmail API messages.
func TestBuildGmailStoredRecord_nil(t *testing.T) {
	r := buildGmailStoredRecord(nil)
	if r.ID != "" {
		t.Fatalf("expected zero record for nil msg, got %#v", r)
	}
}

// Verifies Gmail record building marks messages with detected attachments.
func TestBuildGmailStoredRecord_withAttachments(t *testing.T) {
	rec := buildGmailStoredRecord(&gmail.Message{
		Id: "att1",
		Payload: &gmail.MessagePart{
			MimeType: "multipart/mixed",
			Parts: []*gmail.MessagePart{
				{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: b64("body")}},
				{Filename: "report.pdf", Body: &gmail.MessagePartBody{AttachmentId: "a1"}},
			},
		},
	})
	if rec.HasAttachments != 1 {
		t.Fatalf("expected has_attachments=1, got %d", rec.HasAttachments)
	}
}

// Verifies body-field merging handles empty body_text without clobbering other stored fields.
func TestMergeGmailMessageBodyFields_emptyBodyText(t *testing.T) {
	nextText, nextRaw, nextVis, changed := mergeGmailMessageBodyFields("", "raw content", "")
	if !changed || nextText != "raw content" || nextRaw != "raw content" || nextVis != "raw content" {
		t.Fatalf("expected fill from body_raw when body_text empty, got text=%q raw=%q vis=%q changed=%v",
			nextText, nextRaw, nextVis, changed)
	}
}

// Verifies Gmail body extraction can find HTML bodies inside nested MIME structures.
func TestExtractGmailBody_nestedHTMLPart(t *testing.T) {
	got := extractGmailBody(&gmail.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmail.MessagePart{
			{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: b64("<b>bold</b>")}},
		},
	})
	if got != "bold" {
		t.Fatalf("expected nested html stripped, got %q", got)
	}
}

// Verifies Gmail body extraction survives deeply nested MIME trees.
func TestExtractGmailBody_deepRecursion(t *testing.T) {
	got := extractGmailBody(&gmail.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmail.MessagePart{
			{
				MimeType: "multipart/alternative",
				Parts: []*gmail.MessagePart{
					{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: b64("deep")}},
				},
			},
		},
	})
	if got != "deep" {
		t.Fatalf("expected deep recursion to find plain text, got %q", got)
	}
}

// Verifies Gmail body extraction returns empty output for empty payloads.
func TestExtractGmailBody_empty(t *testing.T) {
	got := extractGmailBody(&gmail.MessagePart{MimeType: "application/octet-stream"})
	if got != "" {
		t.Fatalf("expected empty for non-text payload, got %q", got)
	}
	if extractGmailBody(nil) != "" {
		t.Fatal("expected empty for nil payload")
	}
}

// Verifies visible-body derivation returns empty output for empty inputs.
func TestDeriveVisibleBody_empty(t *testing.T) {
	if deriveVisibleBody("") != "" {
		t.Fatal("expected empty output for empty input")
	}
	if deriveVisibleBody("  \n  ") != "" {
		t.Fatal("expected empty output for whitespace-only input")
	}
}

// Verifies visible-body derivation preserves meaningful forwarded content instead of stripping it all.
func TestDeriveVisibleBody_forwardedMessage(t *testing.T) {
	raw := "FYI see below.\n\n---------- Forwarded message ----------\nFrom: alice@example.com\nDate: 2024-03-01\nSubject: Info"
	got := deriveVisibleBody(raw)
	if strings.Contains(got, "Forwarded message") {
		t.Fatalf("expected forwarded boundary to be stripped, got %q", got)
	}
	if got != "FYI see below." {
		t.Fatalf("expected authored text, got %q", got)
	}
}

// Verifies raw-body fallback triggers when stripping heuristics would discard too much authored content.
func TestShouldFallbackToRaw_aggressiveStripping(t *testing.T) {
	longRaw := strings.Repeat("a", 500)
	shortVisible := "hi"
	if !shouldFallbackToRaw(longRaw, shortVisible) {
		t.Fatal("expected fallback when visible is <2% of long raw")
	}
	medRaw := strings.Repeat("a", 200)
	tinyVis := "x"
	if !shouldFallbackToRaw(medRaw, tinyVis) {
		t.Fatal("expected fallback when raw>=200 and visible<20")
	}
	if shouldFallbackToRaw("short", "short") {
		t.Fatal("expected no fallback for same-length content")
	}
	if shouldFallbackToRaw("", "") {
		t.Fatal("expected no fallback for empty raw")
	}
	if !shouldFallbackToRaw("has content", "") {
		t.Fatal("expected fallback when visible is empty but raw is not")
	}
}

// Verifies quoted-header detection handles representative Gmail reply-header edge cases.
func TestIsQuotedHeaderBlock_edgeCases(t *testing.T) {
	blankThenHeaders := []string{"", "From: alice", "To: bob", "Subject: hi"}
	if !isQuotedHeaderBlock(blankThenHeaders, 0) {
		t.Fatal("expected blank line before headers to still match")
	}
	singleHeader := []string{"From: alice"}
	if isQuotedHeaderBlock(singleHeader, 0) {
		t.Fatal("expected single header to not match (need >=2)")
	}
	nonHeaderBreak := []string{"From: alice", "random text", "To: bob"}
	if isQuotedHeaderBlock(nonHeaderBreak, 0) {
		t.Fatal("expected non-header line to break matching")
	}
	headersThenBlank := []string{"From: alice", "To: bob", "", "Subject: hi"}
	if !isQuotedHeaderBlock(headersThenBlank, 0) {
		t.Fatal("expected blank line after matched headers to still count >=2")
	}
}

// Verifies trailing quoted-block trimming keeps authored content that appears before quoted text.
func TestTrimTrailingQuotedBlock_authoredThenQuoted(t *testing.T) {
	lines := []string{"Hello there", "", "> quoted reply", "> more quoted"}
	result := trimTrailingQuotedBlock(lines)
	if len(result) != 1 || result[0] != "Hello there" {
		t.Fatalf("expected authored content kept, trailing quoted removed, got %v", result)
	}
}

// Verifies authored-content detection returns false when a body consists only of quoted lines.
func TestHasAuthoredContent_allQuoted(t *testing.T) {
	lines := []string{"> quoted", "> also quoted", ""}
	if hasAuthoredContent(lines) {
		t.Fatal("expected no authored content in all-quoted lines")
	}
}

// Verifies mobile-signature trimming handles signatures without a preceding blank line.
func TestTrimTrailingMobileSignature_noBlankBefore(t *testing.T) {
	lines := []string{"text", "Sent from my iPhone"}
	result := trimTrailingMobileSignature(lines)
	if len(result) != 2 {
		t.Fatalf("expected no trimming when blank line missing before signature, got %v", result)
	}
}

// Verifies trailing quoted-block trimming leaves bodies unchanged when no quoted block exists.
func TestTrimTrailingQuotedBlock_noQuotes(t *testing.T) {
	lines := []string{"Hello", "World", ""}
	result := trimTrailingQuotedBlock(lines)
	if len(result) != 2 || result[0] != "Hello" || result[1] != "World" {
		t.Fatalf("expected trailing blanks trimmed, no quoted removal, got %v", result)
	}
}

// Verifies trailing quoted-block trimming handles all-empty inputs safely.
func TestTrimTrailingQuotedBlock_allEmpty(t *testing.T) {
	result := trimTrailingQuotedBlock([]string{"", "  ", ""})
	if len(result) != 0 {
		t.Fatalf("expected all-empty to yield empty, got %v", result)
	}
}

// Verifies trailing quoted-block trimming can remove bodies made entirely of quoted content.
func TestTrimTrailingQuotedBlock_allQuoted(t *testing.T) {
	lines := []string{"> a", "> b", "> c"}
	result := trimTrailingQuotedBlock(lines)
	if len(result) != 0 {
		t.Fatalf("expected all-quoted block to be trimmed entirely, got %v", result)
	}
}

// Verifies thread-chunk building splits oversized single entries instead of dropping them.
func TestBuildGmailThreadChunks_overflowEntry(t *testing.T) {
	hugeBody := strings.Repeat("word ", 1200)
	messages := []gmailMessageRecord{
		{ID: "big", ThreadID: "t1", Subject: "S", From: "a@b.com", Date: "2024-01-01", BodyVisible: hugeBody},
	}
	chunks := buildGmailThreadChunks("S", "a@b.com", messages)
	if len(chunks) < 2 {
		t.Fatalf("expected huge single-message entry to produce multiple chunks, got %d", len(chunks))
	}
}

// Verifies thread-chunk building skips messages whose bodies are empty after cleanup.
func TestBuildGmailThreadChunks_emptyBodySkipped(t *testing.T) {
	messages := []gmailMessageRecord{
		{ID: "empty", ThreadID: "t1", BodyVisible: "", BodyRaw: ""},
	}
	chunks := buildGmailThreadChunks("S", "p", messages)
	if len(chunks) != 0 {
		t.Fatalf("expected empty-body message to be skipped, got %d chunks", len(chunks))
	}
}

// Verifies thread-chunk header formatting handles empty metadata gracefully.
func TestFormatThreadChunkHeader_empty(t *testing.T) {
	h := formatThreadChunkHeader("", "")
	if h != "" {
		t.Fatalf("expected empty header for no subject/participants, got %q", h)
	}
}

// Verifies transcript-entry formatting handles messages whose visible body is empty.
func TestFormatThreadTranscriptEntry_emptyBody(t *testing.T) {
	s := formatThreadTranscriptEntry(gmailMessageRecord{BodyVisible: "", BodyRaw: ""})
	if s != "" {
		t.Fatalf("expected empty string for no-body message, got %q", s)
	}
}

// Verifies transcript-entry formatting falls back to raw body content when visible text is unavailable.
func TestFormatThreadTranscriptEntry_fallbackToRaw(t *testing.T) {
	s := formatThreadTranscriptEntry(gmailMessageRecord{BodyVisible: "", BodyRaw: "raw content", From: "x@y.com", Date: "2024-01-01"})
	if !strings.Contains(s, "raw content") {
		t.Fatalf("expected raw fallback, got %q", s)
	}
}

// Verifies thread-record building skips messages without a usable thread ID.
func TestBuildGmailThreadRecords_skipsEmptyThreadID(t *testing.T) {
	records := buildGmailThreadRecords([]gmailMessageRecord{
		{ID: "orphan", ThreadID: "", Subject: "S"},
	})
	if len(records) != 0 {
		t.Fatalf("expected empty thread_id to be skipped, got %d records", len(records))
	}
}

// Verifies thread-record building returns an empty result when there are no messages to summarize.
func TestBuildThreadRecord_emptyMessages(t *testing.T) {
	r := buildThreadRecord(nil)
	if r.threadID != "" {
		t.Fatalf("expected zero record for empty messages, got %#v", r)
	}
}

// Verifies participant joining deduplicates repeated names while preserving readable output.
func TestJoinParticipants_dedups(t *testing.T) {
	msgs := []gmailMessageRecord{
		{From: "a@b.com, c@d.com", To: "a@b.com"},
	}
	got := joinParticipants(msgs)
	if strings.Count(got, "a@b.com") != 1 {
		t.Fatalf("expected deduplication, got %q", got)
	}
}

// Verifies long-entry splitting leaves entries unchanged when they already fit within the limit.
func TestSplitLongThreadEntry_fitsInLimit(t *testing.T) {
	parts := splitLongThreadEntry("short", 100)
	if len(parts) != 1 || parts[0] != "short" {
		t.Fatalf("expected single part, got %#v", parts)
	}
}

// Verifies long-entry splitting still breaks oversized content when no newline boundary exists.
func TestSplitLongThreadEntry_noNewlineBreak(t *testing.T) {
	entry := strings.Repeat("x", 200)
	parts := splitLongThreadEntry(entry, 50)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts for long no-newline entry, got %d", len(parts))
	}
}

// Verifies orphan deletion removes rows absent from the keep set when keyed by resource name.
func TestDeleteOrphanedRowsByResourceName(t *testing.T) {
	db := newTestDB(t)
	seedContacts(t, db)
	before, _ := countTable(db, "contacts_people")
	if before == 0 {
		t.Fatal("expected seeded contacts")
	}
	deleteOrphanedRowsByResourceName(db, "contacts_people", map[string]bool{})
	after, _ := countTable(db, "contacts_people")
	if after != 0 {
		t.Fatalf("expected all orphaned rows deleted, got %d", after)
	}
}

// Verifies Gmail FTS indexing targets the visible-body column used by search.
func TestGmailFTSIndexesBodyVisible(t *testing.T) {
	db := newTestDB(t)
	if !gmailFTSIndexesBodyVisible(db) {
		t.Fatal("expected FTS to index body_visible after full schema init")
	}
}

// Verifies thread-population detection reports whether Gmail thread rows have been built.
func TestGmailThreadsPopulated(t *testing.T) {
	db := newTestDB(t)
	if gmailThreadsPopulated(db) {
		t.Fatal("expected no threads in empty DB")
	}
	seedGmail(t, db)
	if !gmailThreadsPopulated(db) {
		t.Fatal("expected threads after seeding")
	}
}

// Verifies table-column detection reports schema presence for migration guards.
func TestTableHasColumn(t *testing.T) {
	db := newTestDB(t)
	has, err := tableHasColumn(db, "gmail_messages", "subject")
	if err != nil || !has {
		t.Fatalf("expected gmail_messages to have subject column")
	}
	has, err = tableHasColumn(db, "gmail_messages", "nonexistent_col")
	if err != nil || has {
		t.Fatalf("expected nonexistent column to return false")
	}
}

// Verifies Gmail message ordering handles cases where only one message has a parseable date.
func TestGmailMessageLess_oneHasDate(t *testing.T) {
	withDate := gmailMessageRecord{ID: "b", Date: "2024-01-01T00:00:00Z"}
	withoutDate := gmailMessageRecord{ID: "a", Date: ""}
	if !gmailMessageLess(withDate, withoutDate) {
		t.Fatal("expected message with date to sort before message without date")
	}
	if gmailMessageLess(withoutDate, withDate) {
		t.Fatal("expected message without date to sort after message with date")
	}
}

// Verifies Gmail message ordering compares different parseable date strings correctly.
func TestGmailMessageLess_differentDateStrings(t *testing.T) {
	a := gmailMessageRecord{ID: "x", Date: "aaa"}
	b := gmailMessageRecord{ID: "y", Date: "bbb"}
	if !gmailMessageLess(a, b) {
		t.Fatal("expected lexicographic fallback on unparseable date strings")
	}
}

// Verifies Gmail date parsing and message ordering stay aligned for representative header values.
func TestParseGmailMessageDateAndOrdering(t *testing.T) {
	rfc := parseGmailMessageDate("2024-03-01T10:00:00Z")
	if rfc.IsZero() {
		t.Fatal("expected RFC3339 date to parse")
	}
	mailDate := parseGmailMessageDate("Fri, 01 Mar 2024 11:00:00 +0000")
	if mailDate.IsZero() {
		t.Fatal("expected RFC822-style date to parse")
	}
	if !mailDate.After(rfc) {
		t.Fatalf("expected mail date %v to be after %v", mailDate, rfc)
	}
	if !parseGmailMessageDate("not-a-date").IsZero() {
		t.Fatal("expected invalid date to return zero")
	}

	earlier := gmailMessageRecord{ID: "a", Date: "2024-03-01T10:00:00Z"}
	later := gmailMessageRecord{ID: "b", Date: "Fri, 01 Mar 2024 11:00:00 +0000"}
	if !gmailMessageLess(earlier, later) {
		t.Fatalf("expected %v to sort before %v", earlier, later)
	}

	noDateA := gmailMessageRecord{ID: "a", Date: ""}
	noDateB := gmailMessageRecord{ID: "b", Date: ""}
	if !gmailMessageLess(noDateA, noDateB) {
		t.Fatalf("expected ID fallback ordering for zero dates")
	}
}
