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
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "calendar_events") },
	tablesToDrop:  []string{"calendar_events", "calendar_events_fts"},
}

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
	if err != nil {
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
				if localUpdated == ev.Updated {
					continue
				}
				startTime, endTime, allDay := parseEventTimes(ev)
				organizer := ""
				if ev.Organizer != nil {
					if ev.Organizer.DisplayName != "" {
						organizer = fmt.Sprintf("%s <%s>", ev.Organizer.DisplayName, ev.Organizer.Email)
					} else {
						organizer = ev.Organizer.Email
					}
				}
				var attendeeNames []string
				for _, a := range ev.Attendees {
					if a.DisplayName != "" {
						attendeeNames = append(attendeeNames, fmt.Sprintf("%s <%s>", a.DisplayName, a.Email))
					} else {
						attendeeNames = append(attendeeNames, a.Email)
					}
				}
				recurrence := strings.Join(ev.Recurrence, "; ")
				sctx.DB.Exec(`INSERT OR REPLACE INTO calendar_events
					(id, calendar_id, calendar_name, summary, description, location,
					 start_time, end_time, all_day, created_time, updated_time,
					 organizer, attendees, status, recurrence, html_link, last_synced)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
					eventID, cal.Id, cal.Summary, ev.Summary, ev.Description, ev.Location,
					startTime, endTime, allDay, ev.Created, ev.Updated,
					organizer, strings.Join(attendeeNames, ", "), ev.Status, recurrence, ev.HtmlLink)
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

func registerCalendarTools(src *Source, prefix string, s toolAdder) {
	s.AddTool(mcp.NewTool(prefix+"calendar_search",
		mcp.WithDescription("Search across Google Calendar events"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleCalendarSearch(src, ctx, req)
	})
	s.AddTool(mcp.NewTool(prefix+"calendar_get_event",
		mcp.WithDescription("Get details of a specific calendar event by ID"),
		mcp.WithString("event_id", mcp.Required(), mcp.Description("Calendar event ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleCalendarGetEvent(src, ctx, req)
	})
	s.AddTool(mcp.NewTool(prefix+"calendar_list_upcoming",
		mcp.WithDescription("List upcoming calendar events"),
		mcp.WithNumber("days", mcp.Description("Number of days ahead to look (default 7)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleCalendarListUpcoming(src, ctx, req)
	})
}

func handleCalendarSearch(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
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
	if err != nil {
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

func handleCalendarGetEvent(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	eventID, _ := req.RequireString("event_id")
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
	if err != nil {
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

func handleCalendarListUpcoming(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	if err != nil {
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

func calendarSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT id, summary, description, location, start_time, end_time, organizer, attendees, updated_time
		FROM calendar_events`)
	if err != nil {
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
