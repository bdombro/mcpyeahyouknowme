package gsuite

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Builds an in-memory SQLite DB with the full gsuite schema for isolated source and MCP tests.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)&cache=shared")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	if err := initGSuiteDB(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Builds a source backed by an in-memory DB so tests can exercise handlers without on-disk fixtures.
func newTestSource(t *testing.T) *Source {
	t.Helper()
	db := newTestDB(t)
	return &Source{db: db, dataDir: t.TempDir(), apps: allAppsEnabledConfig()}
}

// Returns an app config with every gsuite app enabled so shared fixtures cover the whole source surface.
func allAppsEnabledConfig() AppsConfig {
	cfg := DefaultAppsConfig()
	for _, app := range allApps {
		cfg.SetEnabled(app.name, true)
	}
	return cfg
}

// Seeds sample Docs rows so search and fetch tests have realistic document fixtures.
func seedDocs(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO docs_documents
		(id, title, content, modified_time, created_time, web_view_link, owners, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"doc1", "Project Proposal", "This is the project proposal content.", "2024-01-15T10:00:00Z",
		"2024-01-01T09:00:00Z", "https://docs.google.com/doc1", "Alice <alice@example.com>")
	if err != nil {
		t.Fatalf("seed doc: %v", err)
	}
}

// Seeds sample Sheets rows so search and fetch tests have realistic spreadsheet fixtures.
func seedSheets(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO sheets_spreadsheets
		(id, title, content, modified_time, created_time, web_view_link, owners, sheet_count, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"sheet1", "Budget 2024", "## Summary\nQ1\t100\nQ2\t200", "2024-02-10T08:00:00Z",
		"2024-02-01T08:00:00Z", "https://sheets.google.com/sheet1", "Bob <bob@example.com>", 2)
	if err != nil {
		t.Fatalf("seed sheet: %v", err)
	}
}

// Seeds sample Gmail messages and rebuilds thread tables so message and thread tools share consistent fixtures.
func seedGmail(t *testing.T, db *sql.DB) {
	t.Helper()
	msg1Raw := "Hi Bob,\n\nCan you make the meeting tomorrow?\n\nThanks,\nAlice"
	msg2Raw := "Yes, I can make it.\n\nOn Fri, Mar 1, 2024 at 10:00 AM Alice <alice@example.com> wrote:\n> Hi Bob,\n> \n> Can you make the meeting tomorrow?\n> \n> Thanks,\n> Alice"
	_, err := db.Exec(`INSERT INTO gmail_messages
		(id, thread_id, labels, folder, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		 date, snippet, body_visible, has_attachments, size_estimate, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now')),
		       (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"msg1", "thread1", "INBOX,UNREAD", "INBOX",
		"Meeting Tomorrow", "alice@example.com", "bob@example.com", "", "",
		"2024-03-01T10:00:00Z", "Can you make the meeting tomorrow?",
		deriveVisibleBody(msg1Raw), 0, 1024,
		"msg2", "thread1", "INBOX", "INBOX",
		"Re: Meeting Tomorrow", "bob@example.com", "alice@example.com", "", "",
		"2024-03-01T11:00:00Z", "Yes, I can make it.",
		deriveVisibleBody(msg2Raw), 0, 2048)
	if err != nil {
		t.Fatalf("seed gmail: %v", err)
	}
	if err := rebuildAllGmailThreads(db); err != nil {
		t.Fatalf("rebuild gmail threads: %v", err)
	}
}

// Seeds sample Calendar events so list and search tests have upcoming event fixtures.
func seedCalendar(t *testing.T, db *sql.DB) {
	t.Helper()
	future := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	futureEnd := time.Now().Add(49 * time.Hour).Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO calendar_events
		(id, calendar_name, summary, description, location,
		 start_time, end_time, all_day, created_time, updated_time,
		 organizer, attendees, status, recurrence, html_link, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"cal1|ev1", "Work", "Team Standup", "Daily standup meeting", "Zoom",
		future, futureEnd, 0, "2024-01-01T00:00:00Z", "2024-01-15T00:00:00Z",
		"alice@example.com", "bob@example.com, carol@example.com", "confirmed", "", "https://calendar.google.com/ev1")
	if err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
}

// Seeds sample Tasks rows so list and search tests have task fixtures.
func seedTasks(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO tasks_items
		(id, tasklist_title, title, notes, status, due, updated, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"task1", "My Tasks", "Write unit tests", "Cover all new gsuite code", "needsAction",
		"2024-04-01T00:00:00Z", "2024-03-20T12:00:00Z")
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

// Seeds sample Contacts rows so people-search tests have realistic directory fixtures.
func seedContacts(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO contacts_people
		(resource_name, display_name, emails, phones, organizations, notes, updated_time, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"people/c1", "Alice Smith", "alice@example.com", "15550100",
		"Acme Corp (Engineer)", "VIP customer", "2024-01-10T00:00:00Z")
	if err != nil {
		t.Fatalf("seed contact: %v", err)
	}
}

// Seeds sample Slides rows so search and fetch tests have presentation fixtures.
func seedSlides(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO slides_presentations
		(id, title, content, modified_time, created_time, web_view_link, owners, slide_count, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"pres1", "Q1 Review", "## Slide 1\nRevenue grew 20%\n## Slide 2\nTeam highlights",
		"2024-03-31T17:00:00Z", "2024-03-01T09:00:00Z",
		"https://slides.google.com/pres1", "carol@example.com", 2)
	if err != nil {
		t.Fatalf("seed slide: %v", err)
	}
}

// Seeds every gsuite fixture set so integration-style tests can exercise cross-app behavior from one DB.
func seedAll(t *testing.T, db *sql.DB) {
	t.Helper()
	seedDocs(t, db)
	seedSheets(t, db)
	seedGmail(t, db)
	seedCalendar(t, db)
	seedTasks(t, db)
	seedContacts(t, db)
	seedSlides(t, db)
}
