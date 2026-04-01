package gsuite

import (
	"database/sql"
	"strings"
	"testing"
)

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

func TestDeriveVisibleBody_fallsBackToRawWhenOnlyQuotedTextRemains(t *testing.T) {
	raw := "> Prior quoted line\n> Another quoted line"
	got := deriveVisibleBody(raw)
	if got != raw {
		t.Fatalf("expected fallback to raw quoted body, got %q", got)
	}
}

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
