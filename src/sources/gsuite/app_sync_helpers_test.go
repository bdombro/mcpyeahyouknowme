package gsuite

import (
	"strings"
	"testing"

	calendarapi "google.golang.org/api/calendar/v3"
	docsapi "google.golang.org/api/docs/v1"
	driveapi "google.golang.org/api/drive/v3"
	peopleapi "google.golang.org/api/people/v1"
	sheetsapi "google.golang.org/api/sheets/v4"
	slidesapi "google.golang.org/api/slides/v1"
	tasksapi "google.golang.org/api/tasks/v1"
)

// Verifies contact-record building maps People API fields into the stored contact shape.
func TestBuildContactRecord(t *testing.T) {
	record := buildContactRecord(&peopleapi.Person{
		ResourceName: "people/123",
		Metadata: &peopleapi.PersonMetadata{
			Sources: []*peopleapi.Source{{UpdateTime: "2026-04-01T10:00:00Z"}},
		},
		Names: []*peopleapi.Name{{
			DisplayName: "Alice Example",
		}},
		EmailAddresses: []*peopleapi.EmailAddress{{Value: "alice@example.com"}},
		PhoneNumbers:   []*peopleapi.PhoneNumber{{Value: "555-0001"}},
		Organizations:  []*peopleapi.Organization{{Name: "Acme", Title: "Manager"}},
		Biographies:    []*peopleapi.Biography{{Value: "Met at conference"}},
	})

	if record.ResourceName != "people/123" || record.DisplayName != "Alice Example" {
		t.Fatalf("unexpected contact record: %#v", record)
	}
	if record.Emails != "alice@example.com" || record.Phones != "5550001" {
		t.Fatalf("expected flattened contact fields, got %#v", record)
	}
	if record.Organizations != "Acme (Manager)" {
		t.Fatalf("expected organization formatting, got %#v", record)
	}
	if record.Notes != "Met at conference" || record.UpdatedTime != "2026-04-01T10:00:00Z" {
		t.Fatalf("expected notes and updated time, got %#v", record)
	}
}

// Verifies docs-record building combines Drive metadata and extracted content into the stored docs shape.
func TestBuildDocsRecord(t *testing.T) {
	record := buildDocsRecord(&driveapi.File{
		Id:           "doc-1",
		Name:         "Project Plan",
		ModifiedTime: "2026-04-01T11:00:00Z",
		CreatedTime:  "2026-03-01T11:00:00Z",
		WebViewLink:  "https://docs.example/doc-1",
		Owners: []*driveapi.User{
			{DisplayName: "Alice", EmailAddress: "alice@example.com"},
			{DisplayName: "Me", EmailAddress: "self@example.com"},
		},
	}, &docsapi.Document{
		Body: &docsapi.Body{
			Content: []*docsapi.StructuralElement{{
				Paragraph: &docsapi.Paragraph{
					Elements: []*docsapi.ParagraphElement{
						{TextRun: &docsapi.TextRun{Content: "Hello "}},
						{TextRun: &docsapi.TextRun{Content: "world"}},
					},
				},
			}},
		},
	}, "self@example.com")

	if record.ID != "doc-1" || record.Title != "Project Plan" {
		t.Fatalf("unexpected docs record: %#v", record)
	}
	if record.Content != "Hello world" {
		t.Fatalf("expected extracted document text, got %q", record.Content)
	}
	if record.Owners != "Alice <alice@example.com>" {
		t.Fatalf("expected self owner to be filtered, got %q", record.Owners)
	}
}

// Verifies sheets-record building combines Drive metadata and extracted sheet text into the stored sheets shape.
func TestBuildSheetsRecord(t *testing.T) {
	record := buildSheetsRecord(&driveapi.File{
		Id:           "sheet-1",
		Name:         "Budget",
		ModifiedTime: "2026-04-01T12:00:00Z",
		CreatedTime:  "2026-03-01T12:00:00Z",
		WebViewLink:  "https://docs.example/sheet-1",
	}, &sheetsapi.Spreadsheet{
		Sheets: []*sheetsapi.Sheet{{
			Properties: &sheetsapi.SheetProperties{Title: "Summary"},
			Data: []*sheetsapi.GridData{{
				RowData: []*sheetsapi.RowData{{
					Values: []*sheetsapi.CellData{
						{FormattedValue: "Revenue"},
						{FormattedValue: "100"},
					},
				}},
			}},
		}},
	}, "")

	if record.ID != "sheet-1" || record.SheetCount != 1 {
		t.Fatalf("unexpected sheets record: %#v", record)
	}
	expected := "## Summary\nRevenue\t100\n"
	if record.Content != expected {
		t.Fatalf("expected extracted sheet text %q, got %q", expected, record.Content)
	}
}

// Verifies slides-record building combines Drive metadata and extracted presentation text into the stored slides shape.
func TestBuildSlidesRecord(t *testing.T) {
	record := buildSlidesRecord(&driveapi.File{
		Id:           "slides-1",
		Name:         "Launch Deck",
		ModifiedTime: "2026-04-01T13:00:00Z",
		CreatedTime:  "2026-03-01T13:00:00Z",
		WebViewLink:  "https://docs.example/slides-1",
	}, &slidesapi.Presentation{
		Slides: []*slidesapi.Page{{
			PageElements: []*slidesapi.PageElement{{
				Shape: &slidesapi.Shape{
					Text: &slidesapi.TextContent{
						TextElements: []*slidesapi.TextElement{
							{TextRun: &slidesapi.TextRun{Content: "Slide title"}},
						},
					},
				},
			}},
		}},
	}, "")

	if record.ID != "slides-1" || record.SlideCount != 1 {
		t.Fatalf("unexpected slides record: %#v", record)
	}
	if record.Content != "## Slide 1\nSlide title" {
		t.Fatalf("expected extracted presentation text, got %q", record.Content)
	}
}

// Verifies task-record building maps Google Tasks fields into the stored task shape.
func TestBuildTaskRecord(t *testing.T) {
	record := buildTaskRecord(&tasksapi.TaskList{
		Id:    "list-1",
		Title: "Personal",
	}, &tasksapi.Task{
		Id:      "task-1",
		Title:   "Buy milk",
		Notes:   "2%",
		Status:  "needsAction",
		Due:     "2026-04-03T00:00:00Z",
		Updated: "2026-04-01T14:00:00Z",
	})

	if record.TasklistTitle != "Personal" {
		t.Fatalf("unexpected task list fields: %#v", record)
	}
	if record.ID != "task-1" || record.Title != "Buy milk" {
		t.Fatalf("unexpected task fields: %#v", record)
	}
}

// Verifies calendar-event record building maps timed events into the stored calendar shape.
func TestBuildCalendarEventRecord(t *testing.T) {
	record := buildCalendarEventRecord(&calendarapi.CalendarListEntry{
		Id:      "cal-1",
		Summary: "Work",
	}, &calendarapi.Event{
		Id:          "evt-1",
		Summary:     "Planning",
		Description: "Quarterly planning review",
		Location:    "Room 1",
		Created:     "2026-03-01T10:00:00Z",
		Updated:     "2026-04-01T15:00:00Z",
		Status:      "confirmed",
		HtmlLink:    "https://calendar.example/evt-1",
		Start:       &calendarapi.EventDateTime{DateTime: "2026-04-02T09:00:00Z"},
		End:         &calendarapi.EventDateTime{DateTime: "2026-04-02T10:00:00Z"},
		Organizer:   &calendarapi.EventOrganizer{DisplayName: "Alice", Email: "alice@example.com"},
		Attendees: []*calendarapi.EventAttendee{
			{DisplayName: "Bob", Email: "bob@example.com"},
			{Email: "carol@example.com"},
		},
		Recurrence: []string{"RRULE:FREQ=WEEKLY"},
	})

	if record.ID != "cal-1|evt-1" || record.CalendarName != "Work" {
		t.Fatalf("unexpected calendar record: %#v", record)
	}
	if record.Organizer != "Alice <alice@example.com>" {
		t.Fatalf("unexpected organizer formatting: %#v", record)
	}
	if record.Attendees != "Bob <bob@example.com>, carol@example.com" {
		t.Fatalf("unexpected attendee formatting: %#v", record)
	}
	if record.StartTime != "2026-04-02T09:00:00Z" || record.AllDay != 0 {
		t.Fatalf("unexpected time parsing: %#v", record)
	}
	if record.Recurrence != "RRULE:FREQ=WEEKLY" {
		t.Fatalf("unexpected recurrence: %#v", record)
	}
}

// Verifies calendar-event record building handles all-day events and nil helper fields safely.
func TestBuildCalendarEventRecord_AllDayAndNilHelpers(t *testing.T) {
	record := buildCalendarEventRecord(nil, &calendarapi.Event{
		Id:    "evt-2",
		Start: &calendarapi.EventDateTime{Date: "2026-04-05"},
		End:   &calendarapi.EventDateTime{Date: "2026-04-06"},
	})

	if record.ID != "evt-2" || record.StartTime != "2026-04-05" || record.EndTime != "2026-04-06" || record.AllDay != 1 {
		t.Fatalf("expected all-day event fields, got %#v", record)
	}
	if formatCalendarOrganizer(nil) != "" {
		t.Fatal("expected nil organizer to format as empty string")
	}
	if got := formatCalendarAttendees([]*calendarapi.EventAttendee{nil}); len(got) != 0 {
		t.Fatalf("expected nil attendees to be skipped, got %#v", got)
	}
}

// Verifies contact-record building returns nil for nil People API input.
func TestBuildContactRecord_nil(t *testing.T) {
	r := buildContactRecord(nil)
	if r.ResourceName != "" {
		t.Fatalf("expected zero record from nil, got %#v", r)
	}
}

// Verifies docs-record building returns nil for nil docs content input.
func TestBuildDocsRecord_nil(t *testing.T) {
	r := buildDocsRecord(nil, nil, "")
	if r.ID != "" {
		t.Fatalf("expected zero record from nil file, got %#v", r)
	}
}

// Verifies sheets-record building returns nil for nil spreadsheet input.
func TestBuildSheetsRecord_nilSpreadsheet(t *testing.T) {
	r := buildSheetsRecord(&driveapi.File{Id: "s1", Name: "T"}, nil, "")
	if r.ID != "s1" || r.SheetCount != 0 || r.Content != "" {
		t.Fatalf("expected nil spreadsheet to yield zero content/count, got %#v", r)
	}
}

// Verifies slides-record building returns nil for nil presentation input.
func TestBuildSlidesRecord_nilPresentation(t *testing.T) {
	r := buildSlidesRecord(&driveapi.File{Id: "p1", Name: "P"}, nil, "")
	if r.ID != "p1" || r.SlideCount != 0 || r.Content != "" {
		t.Fatalf("expected nil presentation to yield zero content/count, got %#v", r)
	}
}

// Verifies task-record building returns nil for nil task input.
func TestBuildTaskRecord_nilTask(t *testing.T) {
	r := buildTaskRecord(&tasksapi.TaskList{Id: "l1"}, nil)
	if r.ID != "" {
		t.Fatalf("expected zero record from nil task, got %#v", r)
	}
}

// Verifies task-record building returns nil when the tasklist context is missing.
func TestBuildTaskRecord_nilTaskList(t *testing.T) {
	r := buildTaskRecord(nil, &tasksapi.Task{Id: "t1", Title: "Buy milk"})
	if r.ID != "t1" || r.TasklistTitle != "" {
		t.Fatalf("expected task without list info, got %#v", r)
	}
}

// Verifies spreadsheet text extraction returns an empty string for nil sheet data.
func TestExtractSpreadsheetText_nilData(t *testing.T) {
	text := extractSpreadsheetText(&sheetsapi.Spreadsheet{
		Sheets: []*sheetsapi.Sheet{
			{Properties: &sheetsapi.SheetProperties{Title: "Sheet1"}, Data: nil},
			{Properties: &sheetsapi.SheetProperties{Title: "Sheet2"}, Data: []*sheetsapi.GridData{
				{RowData: []*sheetsapi.RowData{
					{Values: []*sheetsapi.CellData{{FormattedValue: "A1"}, {FormattedValue: "B1"}}},
				}},
			}},
		},
	})
	if !strings.Contains(text, "Sheet1") || !strings.Contains(text, "Sheet2") || !strings.Contains(text, "A1\tB1") {
		t.Fatalf("expected multi-sheet text with nil data skipped, got %q", text)
	}
}

// Verifies presentation text extraction walks multiple slides and concatenates visible text.
func TestExtractPresentationText_multiSlide(t *testing.T) {
	text := extractPresentationText(&slidesapi.Presentation{
		Slides: []*slidesapi.Page{
			{PageElements: []*slidesapi.PageElement{
				{Shape: &slidesapi.Shape{Text: &slidesapi.TextContent{
					TextElements: []*slidesapi.TextElement{
						{TextRun: &slidesapi.TextRun{Content: "Title"}},
					},
				}}},
			}},
			{PageElements: []*slidesapi.PageElement{
				{Shape: &slidesapi.Shape{Text: &slidesapi.TextContent{
					TextElements: []*slidesapi.TextElement{
						{TextRun: &slidesapi.TextRun{Content: "Body"}},
					},
				}}},
			}},
		},
	})
	if !strings.Contains(text, "Slide 1") || !strings.Contains(text, "Title") || !strings.Contains(text, "Slide 2") || !strings.Contains(text, "Body") {
		t.Fatalf("expected multi-slide text, got %q", text)
	}
}

// Verifies organizer formatting falls back to the email address when no display name is present.
func TestFormatCalendarOrganizer_emailOnly(t *testing.T) {
	got := formatCalendarOrganizer(&calendarapi.EventOrganizer{Email: "org@co.com"})
	if got != "org@co.com" {
		t.Fatalf("expected email-only organizer, got %q", got)
	}
}

// Verifies organizer formatting prefers the display name when one is available.
func TestFormatCalendarOrganizer_withDisplayName(t *testing.T) {
	got := formatCalendarOrganizer(&calendarapi.EventOrganizer{DisplayName: "Alice", Email: "alice@co.com"})
	if got != "Alice <alice@co.com>" {
		t.Fatalf("expected formatted organizer, got %q", got)
	}
}

// Verifies calendar-event record building returns nil when the calendar context is missing.
func TestBuildCalendarEventRecord_nilCalendar(t *testing.T) {
	ev := &calendarapi.Event{Id: "ev1", Summary: "Meeting"}
	r := buildCalendarEventRecord(nil, ev)
	if r.Summary != "Meeting" || r.CalendarName != "" {
		t.Fatalf("expected event data without calendar info, got %#v", r)
	}
}

// Verifies calendar-event record building returns nil when the event payload is missing.
func TestBuildCalendarEventRecord_nilEvent(t *testing.T) {
	r := buildCalendarEventRecord(&calendarapi.CalendarListEntry{Id: "cal1"}, nil)
	if r.ID != "" {
		t.Fatalf("expected zero record for nil event, got %#v", r)
	}
}

// Verifies docs-record building returns nil when Drive metadata is missing.
func TestBuildDocsRecord_nilDriveFile(t *testing.T) {
	r := buildDocsRecord(nil, &docsapi.Document{Title: "T"}, "me@co.com")
	if r.ID != "" || r.Title != "" {
		t.Fatalf("expected zero record for nil drive file, got %#v", r)
	}
}

// Verifies sheets-record building returns nil when Drive metadata is missing.
func TestBuildSheetsRecord_nilDriveFile(t *testing.T) {
	r := buildSheetsRecord(nil, nil, "")
	if r.ID != "" {
		t.Fatalf("expected zero record for nil drive file, got %#v", r)
	}
}

// Verifies slides-record building returns nil when Drive metadata is missing.
func TestBuildSlidesRecord_nilDriveFile(t *testing.T) {
	r := buildSlidesRecord(nil, nil, "")
	if r.ID != "" {
		t.Fatalf("expected zero record for nil drive file, got %#v", r)
	}
}

// Verifies drive-owner formatting handles empty and mixed owner edge cases predictably.
func TestFormatDriveOwners_edgeCases(t *testing.T) {
	emailOnly := formatDriveOwners([]*driveapi.User{
		{EmailAddress: "a@b.com"},
	}, "")
	if emailOnly != "a@b.com" {
		t.Fatalf("expected email-only owner, got %q", emailOnly)
	}
	nameOnly := formatDriveOwners([]*driveapi.User{
		{DisplayName: "Alice"},
	}, "")
	if nameOnly != "Alice" {
		t.Fatalf("expected name-only owner, got %q", nameOnly)
	}
	empty := formatDriveOwners([]*driveapi.User{{}}, "")
	if empty != "" {
		t.Fatalf("expected empty for no-name-no-email owner, got %q", empty)
	}
}
