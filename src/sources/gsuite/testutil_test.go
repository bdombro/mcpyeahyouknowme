package gsuite

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestDB creates an in-memory SQLite DB with the full gsuite schema.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_fk=on&cache=shared")
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

// newTestSource creates a Source backed by an in-memory DB (no files).
func newTestSource(t *testing.T) *Source {
	t.Helper()
	db := newTestDB(t)
	return &Source{db: db, dataDir: t.TempDir(), apps: DefaultAppsConfig()}
}

// seedDocs inserts sample documents into the test DB.
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

// seedSheets inserts sample spreadsheets into the test DB.
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

// seedGmail inserts sample Gmail messages into the test DB.
func seedGmail(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO gmail_messages
		(id, thread_id, labels, folder, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		 date, snippet, body_text, has_attachments, size_estimate, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"msg1", "thread1", "INBOX,UNREAD", "INBOX",
		"Meeting Tomorrow", "alice@example.com", "bob@example.com", "", "",
		"2024-03-01T10:00:00Z", "Let me know if you can make it.",
		"Hi Bob, let me know if you can make the meeting tomorrow.", 0, 1024)
	if err != nil {
		t.Fatalf("seed gmail: %v", err)
	}
}

// seedCalendar inserts sample calendar events into the test DB.
func seedCalendar(t *testing.T, db *sql.DB) {
	t.Helper()
	future := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	futureEnd := time.Now().Add(49 * time.Hour).Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO calendar_events
		(id, calendar_id, calendar_name, summary, description, location,
		 start_time, end_time, all_day, created_time, updated_time,
		 organizer, attendees, status, recurrence, html_link, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"cal1|ev1", "cal1", "Work", "Team Standup", "Daily standup meeting", "Zoom",
		future, futureEnd, 0, "2024-01-01T00:00:00Z", "2024-01-15T00:00:00Z",
		"alice@example.com", "bob@example.com, carol@example.com", "confirmed", "", "https://calendar.google.com/ev1")
	if err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
}

// seedTasks inserts sample tasks into the test DB.
func seedTasks(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO tasks_items
		(id, tasklist_id, tasklist_title, title, notes, status, due, completed, updated, position, parent, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"task1", "list1", "My Tasks", "Write unit tests", "Cover all new gsuite code", "needsAction",
		"2024-04-01T00:00:00Z", "", "2024-03-20T12:00:00Z", "00000000001", "")
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

// seedContacts inserts sample contacts into the test DB.
func seedContacts(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO contacts_people
		(resource_name, display_name, given_name, family_name, emails, phones, organizations, addresses, notes, updated_time, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"people/c1", "Alice Smith", "Alice", "Smith", "alice@example.com", "+1-555-0100",
		"Acme Corp (Engineer)", "123 Main St", "VIP customer", "2024-01-10T00:00:00Z")
	if err != nil {
		t.Fatalf("seed contact: %v", err)
	}
}

// seedSlides inserts sample presentations into the test DB.
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

// seedAll inserts all sample data into the test DB.
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
