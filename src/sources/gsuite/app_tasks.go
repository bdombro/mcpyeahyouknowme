package gsuite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/api/option"
	"google.golang.org/api/tasks/v1"
)

var tasksAppDef = &appDef{
	name:          "tasks",
	displayName:   "Google Tasks",
	initSchema:    initTasksSchema,
	syncFunc:      syncTasks,
	registerTools: registerTasksTools,
	searchEntries: tasksSearchEntries,
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "tasks_items") }, // nocov
	tablesToDrop:  []string{"tasks_items", "tasks_items_fts"},
}

func initTasksSchema(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS tasks_items (
		id TEXT PRIMARY KEY,
		tasklist_id TEXT NOT NULL DEFAULT '',
		tasklist_title TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		due TEXT NOT NULL DEFAULT '',
		completed TEXT NOT NULL DEFAULT '',
		updated TEXT NOT NULL DEFAULT '',
		position TEXT NOT NULL DEFAULT '',
		parent TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS tasks_items_fts USING fts5(
		title, notes, tasklist_title,
		content='tasks_items',
		content_rowid='rowid'
	);
	CREATE TRIGGER IF NOT EXISTS tasks_items_ai AFTER INSERT ON tasks_items BEGIN
		INSERT INTO tasks_items_fts(rowid, title, notes, tasklist_title)
		VALUES (new.rowid, new.title, new.notes, new.tasklist_title);
	END;
	CREATE TRIGGER IF NOT EXISTS tasks_items_ad AFTER DELETE ON tasks_items BEGIN
		DELETE FROM tasks_items_fts WHERE rowid = old.rowid;
	END;
	CREATE TRIGGER IF NOT EXISTS tasks_items_au AFTER UPDATE ON tasks_items BEGIN
		INSERT INTO tasks_items_fts(tasks_items_fts, rowid, title, notes, tasklist_title)
		VALUES('delete', old.rowid, old.title, old.notes, old.tasklist_title);
		INSERT INTO tasks_items_fts(rowid, title, notes, tasklist_title)
		VALUES (new.rowid, new.title, new.notes, new.tasklist_title);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	db.Exec("INSERT INTO tasks_items_fts(tasks_items_fts) VALUES('rebuild')")
	return nil
}

func syncTasks(sctx syncContext) error { // nocov
	ctx := sctx.Ctx.(context.Context)
	sctx.SetStatus("syncing")
	defer sctx.SetStatus("idle")

	tasksSvc, err := tasks.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Tasks service: %w", err)
	}

	lists, err := tasksSvc.Tasklists.List().MaxResults(100).Do()
	if err != nil { // nocov
		return fmt.Errorf("failed to list task lists: %w", err)
	}

	remoteIDs := make(map[string]bool)
	var updatedCount int
	for _, tl := range lists.Items {
		pageToken := ""
		for {
			call := tasksSvc.Tasks.List(tl.Id).MaxResults(100).ShowCompleted(true).ShowHidden(true)
			if pageToken != "" { // nocov
				call = call.PageToken(pageToken)
			}
			taskList, err := call.Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to list tasks for %s: %v\n", tl.Title, err)
				break
			}
			for _, task := range taskList.Items {
				remoteIDs[task.Id] = true
				var localUpdated string
				sctx.DB.QueryRow("SELECT updated FROM tasks_items WHERE id = ?", task.Id).Scan(&localUpdated)
				record := buildTaskRecord(tl, task)
				if localUpdated == record.Updated {
					continue
				}
				sctx.DB.Exec(`INSERT OR REPLACE INTO tasks_items
					(id, tasklist_id, tasklist_title, title, notes, status, due, completed,
					 updated, position, parent, last_synced)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
					record.ID, record.TasklistID, record.TasklistTitle, record.Title, record.Notes,
					record.Status, record.Due, record.Completed, record.Updated, record.Position,
					record.Parent)
				updatedCount++
			}
			pageToken = taskList.NextPageToken
			if pageToken == "" {
				break
			}
		}
	}
	deleteOrphanedRows(sctx.DB, "tasks_items", remoteIDs)
	fmt.Printf("Google Tasks sync: %d updated\n", updatedCount)
	return nil
}

type taskRecord struct {
	ID            string
	TasklistID    string
	TasklistTitle string
	Title         string
	Notes         string
	Status        string
	Due           string
	Completed     string
	Updated       string
	Position      string
	Parent        string
}

func buildTaskRecord(taskList *tasks.TaskList, task *tasks.Task) taskRecord {
	record := taskRecord{}
	if task == nil {
		return record
	}
	record.ID = task.Id
	record.Title = task.Title
	record.Notes = task.Notes
	record.Status = task.Status
	record.Due = task.Due
	record.Updated = task.Updated
	record.Position = task.Position
	record.Parent = task.Parent
	if task.Completed != nil {
		record.Completed = *task.Completed
	}
	if taskList != nil {
		record.TasklistID = taskList.Id
		record.TasklistTitle = taskList.Title
	}
	return record
}

func registerTasksTools(src *Source, prefix string, s toolAdder) {
	s.AddTool(core.NewReadOnlyTool(prefix+"tasks_search",
		core.ToolDescription("Search across Google Tasks", `{"query":"submit expense report","limit":5}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleTasksSearch(ctx, src, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"tasks_list",
		core.ToolDescription("List tasks, optionally filtered by status", `{"status":"needsAction","limit":10}`),
		mcp.WithString("status", mcp.Description("Filter by status: 'needsAction' or 'completed'")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleTasksList(ctx, src, req)
	})
}

func handleTasksSearch(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, errResult := core.RequireStringArgument(req, "query", `{"query":"submit expense report","limit":5}`)
	if errResult != nil {
		return errResult, nil
	}
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT t.id, t.title, t.notes, t.status, t.due, t.tasklist_title, t.updated
		FROM tasks_items_fts
		JOIN tasks_items t ON t.rowid = tasks_items_fts.rowid
		WHERE tasks_items_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, notes, status, due, listTitle, updated string
		if err := rows.Scan(&id, &title, &notes, &status, &due, &listTitle, &updated); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "notes": notes, "status": status,
			"due": due, "tasklist": listTitle, "updated": updated,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

func handleTasksList(_ context.Context, src *Source, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 20)
	status, _ := req.GetArguments()["status"].(string)
	if src.db == nil {
		return mcp.NewToolResultText("{\"tasks\":[],\"count\":0}"), nil
	}
	var rows *sql.Rows
	var err error
	if status != "" {
		rows, err = src.db.Query(`SELECT id, title, notes, status, due, tasklist_title, updated
			FROM tasks_items WHERE status = ? ORDER BY updated DESC LIMIT ?`, status, limit)
	} else {
		rows, err = src.db.Query(`SELECT id, title, notes, status, due, tasklist_title, updated
			FROM tasks_items ORDER BY updated DESC LIMIT ?`, limit)
	}
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list tasks: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, title, notes, st, due, listTitle, updated string
		if err := rows.Scan(&id, &title, &notes, &st, &due, &listTitle, &updated); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "title": title, "notes": notes, "status": st,
			"due": due, "tasklist": listTitle, "updated": updated,
		})
	}
	return core.JsonResult(map[string]interface{}{"tasks": results, "count": len(results)})
}

func tasksSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT id, title, notes, status, due, tasklist_title, updated FROM tasks_items`)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()
	var entries []core.SearchEntry
	for rows.Next() {
		var id, title, notes, status, due, listTitle, updated string
		if err := rows.Scan(&id, &title, &notes, &status, &due, &listTitle, &updated); err != nil { // nocov
			continue
		}
		meta, _ := json.Marshal(map[string]interface{}{
			"task_id": id, "status": status, "due": due, "tasklist": listTitle, "updated": updated,
		})
		content := title
		if notes != "" {
			content += "\n" + notes
		}
		entries = append(entries, core.SearchEntry{
			Source: sourceName, SourceID: id, ContentType: "task",
			Title: title, Content: content, Metadata: meta,
		})
	}
	return entries, nil
}
