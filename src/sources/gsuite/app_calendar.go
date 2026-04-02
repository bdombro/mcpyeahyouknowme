// Package gsuite implements the Google Workspace data source and MCP tools.
package gsuite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

var calendarAppDef = &appDef{
	name:          "calendar",
	displayName:   "Google Calendar",
	initSchema:    initCalendarSchema,
	syncFunc:      syncCalendar,
	registerTools: registerCalendarTools,
	searchEntries: calendarSearchEntries,
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "calendar_events") }, // nocov
	tablesToDrop:  []string{"calendar_events", "calendar_events_fts"},
}

// initCalendarSchema creates the Calendar tables, FTS index, and triggers used by sync and MCP reads.
func initCalendarSchema(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS calendar_events (
		id TEXT PRIMARY KEY,
		calendar_id TEXT NOT NULL DEFAULT '',
		calendar_name TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		location TEXT NOT NULL DEFAULT '',
		start_time TEXT NOT NULL DEFAULT '',
		end_time TEXT NOT NULL DEFAULT '',
		all_day INTEGER NOT NULL DEFAULT 0,
		created_time TEXT NOT NULL DEFAULT '',
		updated_time TEXT NOT NULL DEFAULT '',
		organizer TEXT NOT NULL DEFAULT '',
		attendees TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		recurrence TEXT NOT NULL DEFAULT '',
		html_link TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS calendar_events_fts USING fts5(
		summary, description, location, organizer, attendees,
		content='calendar_events',
		content_rowid='rowid'
	);
	CREATE TRIGGER IF NOT EXISTS calendar_events_ai AFTER INSERT ON calendar_events BEGIN
		INSERT INTO calendar_events_fts(rowid, summary, description, location, organizer, attendees)
		VALUES (new.rowid, new.summary, new.description, new.location, new.organizer, new.attendees);
	END;
	CREATE TRIGGER IF NOT EXISTS calendar_events_ad AFTER DELETE ON calendar_events BEGIN
		DELETE FROM calendar_events_fts WHERE rowid = old.rowid;
	END;
	CREATE TRIGGER IF NOT EXISTS calendar_events_au AFTER UPDATE ON calendar_events BEGIN
		INSERT INTO calendar_events_fts(calendar_events_fts, rowid, summary, description, location, organizer, attendees)
		VALUES('delete', old.rowid, old.summary, old.description, old.location, old.organizer, old.attendees);
		INSERT INTO calendar_events_fts(rowid, summary, description, location, organizer, attendees)
		VALUES (new.rowid, new.summary, new.description, new.location, new.organizer, new.attendees);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	db.Exec("INSERT INTO calendar_events_fts(calendar_events_fts) VALUES('rebuild')")
	return nil
}

// syncCalendar refreshes recent calendar events into SQLite and removes local rows absent from the latest API window.
func syncCalendar(sctx syncContext) error { // nocov
	ctx := sctx.Ctx.(context.Context)
	sctx.SetStatus("syncing")
	defer sctx.SetStatus("idle")

	calService, err := calendar.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Calendar service: %w", err)
	}

	calList, err := calService.CalendarList.List().Do()
	if err != nil { // nocov
		return fmt.Errorf("failed to list calendars: %w", err)
	}

	now := time.Now()
	timeMin := now.AddDate(-1, 0, 0).Format(time.RFC3339)
	timeMax := now.AddDate(1, 0, 0).Format(time.RFC3339)

	remoteIDs := make(map[string]bool)
	var updatedCount int
	for _, cal := range calList.Items {
		pageToken := ""
		for {
			call := calService.Events.List(cal.Id).
				TimeMin(timeMin).TimeMax(timeMax).
				SingleEvents(true).MaxResults(250)
			if pageToken != "" { // nocov
				call = call.PageToken(pageToken)
			}
			events, err := call.Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to list events for calendar %s: %v\n", cal.Summary, err)
				break
			}
			for _, ev := range events.Items {
				eventID := cal.Id + "|" + ev.Id
				remoteIDs[eventID] = true
				var localUpdated string
				sctx.DB.QueryRow("SELECT updated_time FROM calendar_events WHERE id = ?", eventID).Scan(&localUpdated)
				record := buildCalendarEventRecord(cal, ev)
				if localUpdated == record.UpdatedTime {
					continue
				}
				sctx.DB.Exec(`INSERT OR REPLACE INTO calendar_events
					(id, calendar_id, calendar_name, summary, description, location,
					 start_time, end_time, all_day, created_time, updated_time,
					 organizer, attendees, status, recurrence, html_link, last_synced)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
					record.ID, record.CalendarID, record.CalendarName, record.Summary, record.Description, record.Location,
					record.StartTime, record.EndTime, record.AllDay, record.CreatedTime, record.UpdatedTime,
					record.Organizer, record.Attendees, record.Status, record.Recurrence, record.HTMLLink)
				updatedCount++
			}
			pageToken = events.NextPageToken
			if pageToken == "" {
				break
			}
		}
	}
	deleteOrphanedRows(sctx.DB, "calendar_events", remoteIDs)
	fmt.Printf("Google Calendar sync: %d updated\n", updatedCount)
	return nil
}

type calendarEventRecord struct {
	ID           string
	CalendarID   string
	CalendarName string
	Summary      string
	Description  string
	Location     string
	StartTime    string
	EndTime      string
	AllDay       int
	CreatedTime  string
	UpdatedTime  string
	Organizer    string
	Attendees    string
	Status       string
	Recurrence   string
	HTMLLink     string
}

// buildCalendarEventRecord flattens Calendar API data into one stored event row for SQLite.
func buildCalendarEventRecord(cal *calendar.CalendarListEntry, ev *calendar.Event) calendarEventRecord {
	record := calendarEventRecord{}
	if ev == nil {
		return record
	}
	record.ID = ev.Id
	record.Summary = ev.Summary
	record.Description = ev.Description
	record.Location = ev.Location
	record.CreatedTime = ev.Created
	record.UpdatedTime = ev.Updated
	record.Status = ev.Status
	record.HTMLLink = ev.HtmlLink
	record.StartTime, record.EndTime, record.AllDay = parseEventTimes(ev)
	record.Organizer = formatCalendarOrganizer(ev.Organizer)
	record.Attendees = strings.Join(formatCalendarAttendees(ev.Attendees), ", ")
	record.Recurrence = strings.Join(ev.Recurrence, "; ")
	if cal != nil {
		record.CalendarID = cal.Id
		record.CalendarName = cal.Summary
		if ev.Id != "" {
			record.ID = cal.Id + "|" + ev.Id
		}
	}
	return record
}

// formatCalendarOrganizer formats organizer identity so search and get-event return a readable owner string.
func formatCalendarOrganizer(org *calendar.EventOrganizer) string {
	if org == nil {
		return ""
	}
	if org.DisplayName != "" {
		return fmt.Sprintf("%s <%s>", org.DisplayName, org.Email)
	}
	return org.Email
}

// formatCalendarAttendees formats attendee identities into readable strings for storage and search.
func formatCalendarAttendees(attendees []*calendar.EventAttendee) []string {
	names := make([]string, 0, len(attendees))
	for _, attendee := range attendees {
		if attendee == nil {
			continue
		}
		if attendee.DisplayName != "" {
			names = append(names, fmt.Sprintf("%s <%s>", attendee.DisplayName, attendee.Email))
			continue
		}
		names = append(names, attendee.Email)
	}
	return names
}

// parseEventTimes normalizes all-day and timed event boundaries into stored strings plus an all-day flag.
func parseEventTimes(ev *calendar.Event) (start, end string, allDay int) {
	if ev.Start != nil {
		if ev.Start.Date != "" {
			start = ev.Start.Date
			allDay = 1
		} else {
			start = ev.Start.DateTime
		}
	}
	if ev.End != nil {
		if ev.End.Date != "" {
			end = ev.End.Date
		} else {
			end = ev.End.DateTime
		}
	}
	return
}

// registerCalendarTools wires the local-DB Calendar read tools into MCP so clients query synced events instead of the live API.
func registerCalendarTools(src *Source, prefix string, s toolAdder) {
	s.AddTool(core.NewReadOnlyTool(prefix+"calendar_search",
		core.ToolDescription("Search across Google Calendar events", `{"query":"dentist","limit":5}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleCalendarSearch(ctx, src, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"calendar_get_event",
		core.ToolDescription("Get details of a specific calendar event by ID", `{"event_id":"abc123"}`),
		mcp.WithString("event_id", mcp.Required(), mcp.Description("Calendar event ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleCalendarGetEvent(ctx, src, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"calendar_list_upcoming",
		core.ToolDescription("List upcoming calendar events", `{"days":14,"limit":10}`),
		mcp.WithNumber("days", mcp.Description("Number of days ahead to look (default 7)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleCalendarListUpcoming(ctx, src, req)
	})
}

// handleCalendarSearch runs local FTS for req `query`/`limit`, returning synced event hits with scheduling metadata for follow-up.
func handleCalendarSearch(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, errResult := core.RequireStringArgument(req, "query", `{"query":"dentist","limit":5}`)
	if errResult != nil {
		return errResult, nil
	}
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT e.id, e.summary, e.start_time, e.end_time, e.all_day, e.location, e.organizer, e.calendar_name,
		       snippet(calendar_events_fts, 1, '<mark>', '</mark>', '...', 32) as match_snippet
		FROM calendar_events_fts
		JOIN calendar_events e ON e.rowid = calendar_events_fts.rowid
		WHERE calendar_events_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, summary, start, end, location, organizer, calName, snippet string
		var allDay int
		if err := rows.Scan(&id, &summary, &start, &end, &allDay, &location, &organizer, &calName, &snippet); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "summary": summary, "start_time": start, "end_time": end,
			"all_day": allDay == 1, "location": location, "organizer": organizer,
			"calendar": calName, "snippet": snippet,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

// handleCalendarGetEvent looks up req `event_id` in SQLite and returns the full stored event record, or a tool error if missing.
func handleCalendarGetEvent(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	eventID, errResult := core.RequireStringArgument(req, "event_id", `{"event_id":"abc123"}`)
	if errResult != nil {
		return errResult, nil
	}
	if src.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}
	var summary, desc, location, start, end, created, updated, organizer, attendees, status, recurrence, link, calName string
	var allDay int
	err := src.db.QueryRow(`SELECT summary, description, location, start_time, end_time, all_day,
		created_time, updated_time, organizer, attendees, status, recurrence, html_link, calendar_name
		FROM calendar_events WHERE id = ?`, eventID).
		Scan(&summary, &desc, &location, &start, &end, &allDay, &created, &updated,
			&organizer, &attendees, &status, &recurrence, &link, &calName)
	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Event not found"), nil
	}
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve event: %v", err)), nil
	}
	return core.JsonResult(map[string]interface{}{
		"id": eventID, "summary": summary, "description": desc, "location": location,
		"start_time": start, "end_time": end, "all_day": allDay == 1,
		"created_time": created, "updated_time": updated, "organizer": organizer,
		"attendees": attendees, "status": status, "recurrence": recurrence,
		"link": link, "calendar": calName,
	})
}

// handleCalendarListUpcoming returns synced events between now and req `days`, capped by req `limit`, for upcoming-schedule views.
func handleCalendarListUpcoming(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	days := core.IntArg(req.GetArguments(), "days", 7)
	limit := core.IntArg(req.GetArguments(), "limit", 20)
	if src.db == nil {
		return mcp.NewToolResultText("{\"events\":[],\"count\":0}"), nil
	}
	cutoff := time.Now().AddDate(0, 0, days).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)
	rows, err := src.db.Query(`SELECT id, summary, start_time, end_time, all_day, location, organizer, calendar_name
		FROM calendar_events WHERE start_time >= ? AND start_time <= ?
		ORDER BY start_time ASC LIMIT ?`, now, cutoff, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list events: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, summary, start, end, location, organizer, calName string
		var allDay int
		if err := rows.Scan(&id, &summary, &start, &end, &allDay, &location, &organizer, &calName); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "summary": summary, "start_time": start, "end_time": end,
			"all_day": allDay == 1, "location": location, "organizer": organizer, "calendar": calName,
		})
	}
	return core.JsonResult(map[string]interface{}{"events": results, "count": len(results)})
}

// calendarSearchEntries turns synced event rows into summary and description entries so global search can rank calendar matches well.
func calendarSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT id, summary, description, location, start_time, end_time, organizer, attendees, updated_time
		FROM calendar_events`)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()
	var entries []core.SearchEntry
	for rows.Next() {
		var id, summary, desc, location, start, end, organizer, attendees, updated string
		if err := rows.Scan(&id, &summary, &desc, &location, &start, &end, &organizer, &attendees, &updated); err != nil { // nocov
			continue
		}
		meta, _ := json.Marshal(map[string]interface{}{
			"event_id": id, "start_time": start, "end_time": end, "updated_time": updated,
		})
		content := summary
		if location != "" {
			content += " @ " + location
		}
		if organizer != "" {
			content += " — " + organizer
		}
		entries = append(entries, core.SearchEntry{
			Source: sourceName, SourceID: id, ContentType: "calendar_event",
			Title: summary, Content: content, Metadata: meta,
		})
		if desc != "" {
			descMeta, _ := json.Marshal(map[string]interface{}{
				"event_id": id, "summary": summary, "start_time": start,
			})
			entries = append(entries, core.SearchEntry{
				Source: sourceName, SourceID: id, ContentType: "calendar_event_description",
				Title: summary, Content: desc, Metadata: descMeta,
			})
		}
	}
	return entries, nil
}
