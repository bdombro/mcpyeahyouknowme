package gsuite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"
)

var contactsAppDef = &appDef{
	name:          "contacts",
	displayName:   "Google Contacts",
	initSchema:    initContactsSchema,
	syncFunc:      syncContacts,
	registerTools: registerContactsTools,
	searchEntries: contactsSearchEntries,
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "contacts_people") }, // nocov
	tablesToDrop:  []string{"contacts_people", "contacts_people_fts"},
}

func initContactsSchema(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS contacts_people (
		resource_name TEXT PRIMARY KEY,
		display_name TEXT NOT NULL DEFAULT '',
		given_name TEXT NOT NULL DEFAULT '',
		family_name TEXT NOT NULL DEFAULT '',
		emails TEXT NOT NULL DEFAULT '',
		phones TEXT NOT NULL DEFAULT '',
		organizations TEXT NOT NULL DEFAULT '',
		addresses TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		updated_time TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS contacts_people_fts USING fts5(
		display_name, emails, phones, organizations, notes,
		content='contacts_people',
		content_rowid='rowid'
	);
	CREATE TRIGGER IF NOT EXISTS contacts_people_ai AFTER INSERT ON contacts_people BEGIN
		INSERT INTO contacts_people_fts(rowid, display_name, emails, phones, organizations, notes)
		VALUES (new.rowid, new.display_name, new.emails, new.phones, new.organizations, new.notes);
	END;
	CREATE TRIGGER IF NOT EXISTS contacts_people_ad AFTER DELETE ON contacts_people BEGIN
		DELETE FROM contacts_people_fts WHERE rowid = old.rowid;
	END;
	CREATE TRIGGER IF NOT EXISTS contacts_people_au AFTER UPDATE ON contacts_people BEGIN
		INSERT INTO contacts_people_fts(contacts_people_fts, rowid, display_name, emails, phones, organizations, notes)
		VALUES('delete', old.rowid, old.display_name, old.emails, old.phones, old.organizations, old.notes);
		INSERT INTO contacts_people_fts(rowid, display_name, emails, phones, organizations, notes)
		VALUES (new.rowid, new.display_name, new.emails, new.phones, new.organizations, new.notes);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	db.Exec("INSERT INTO contacts_people_fts(contacts_people_fts) VALUES('rebuild')")
	return nil
}

func syncContacts(sctx syncContext) error { // nocov
	ctx := sctx.Ctx.(context.Context)
	sctx.SetStatus("syncing")
	defer sctx.SetStatus("idle")

	peopleSvc, err := people.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create People service: %w", err)
	}

	remoteIDs := make(map[string]bool)
	var updatedCount int
	pageToken := ""
	for {
		call := peopleSvc.People.Connections.List("people/me").
			PersonFields("names,emailAddresses,phoneNumbers,organizations,addresses,biographies,metadata").
			PageSize(1000)
		if pageToken != "" { // nocov
			call = call.PageToken(pageToken)
		}
		res, err := call.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list contacts: %w", err)
		}
		for _, p := range res.Connections {
			remoteIDs[p.ResourceName] = true
			record := buildContactRecord(p)
			var localUpdated string
			sctx.DB.QueryRow("SELECT updated_time FROM contacts_people WHERE resource_name = ?", p.ResourceName).Scan(&localUpdated)
			if localUpdated == record.UpdatedTime && record.UpdatedTime != "" {
				continue
			}
			sctx.DB.Exec(`INSERT OR REPLACE INTO contacts_people
				(resource_name, display_name, given_name, family_name, emails, phones,
				 organizations, addresses, notes, updated_time, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
				record.ResourceName, record.DisplayName, record.GivenName, record.FamilyName,
				record.Emails, record.Phones, record.Organizations, record.Addresses,
				record.Notes, record.UpdatedTime)
			updatedCount++
		}
		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}
	deleteOrphanedRowsByResourceName(sctx.DB, "contacts_people", remoteIDs)
	fmt.Printf("Google Contacts sync: %d updated\n", updatedCount)
	return nil
}

type contactRecord struct {
	ResourceName  string
	DisplayName   string
	GivenName     string
	FamilyName    string
	Emails        string
	Phones        string
	Organizations string
	Addresses     string
	Notes         string
	UpdatedTime   string
}

func buildContactRecord(p *people.Person) contactRecord {
	record := contactRecord{}
	if p == nil {
		return record
	}
	record.ResourceName = p.ResourceName
	if p.Metadata != nil && len(p.Metadata.Sources) > 0 {
		record.UpdatedTime = p.Metadata.Sources[0].UpdateTime
	}
	if len(p.Names) > 0 {
		record.DisplayName = p.Names[0].DisplayName
		record.GivenName = p.Names[0].GivenName
		record.FamilyName = p.Names[0].FamilyName
	}
	var emails, phones, orgs, addrs []string
	for _, e := range p.EmailAddresses {
		emails = append(emails, e.Value)
	}
	for _, ph := range p.PhoneNumbers {
		phones = append(phones, ph.Value)
	}
	for _, o := range p.Organizations {
		org := o.Name
		if o.Title != "" {
			org += " (" + o.Title + ")"
		}
		orgs = append(orgs, org)
	}
	for _, a := range p.Addresses {
		addrs = append(addrs, a.FormattedValue)
	}
	if len(p.Biographies) > 0 {
		record.Notes = p.Biographies[0].Value
	}
	record.Emails = strings.Join(emails, ", ")
	record.Phones = strings.Join(phones, ", ")
	record.Organizations = strings.Join(orgs, ", ")
	record.Addresses = strings.Join(addrs, "; ")
	return record
}

func deleteOrphanedRowsByResourceName(db *sql.DB, table string, remoteIDs map[string]bool) {
	rows, err := db.Query("SELECT resource_name FROM " + table)
	if err != nil { // nocov
		return
	}
	defer rows.Close()
	var toDelete []string
	for rows.Next() {
		var rn string
		if err := rows.Scan(&rn); err != nil { // nocov
			continue
		}
		if !remoteIDs[rn] {
			toDelete = append(toDelete, rn)
		}
	}
	rows.Close()
	for _, rn := range toDelete {
		db.Exec("DELETE FROM "+table+" WHERE resource_name = ?", rn)
	}
}

func registerContactsTools(src *Source, prefix string, s toolAdder) {
	s.AddTool(core.NewReadOnlyTool(prefix+"contacts_search",
		core.ToolDescription("Search across Google Contacts", `{"query":"Alice Smith","limit":5}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleContactsSearch(src, ctx, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"contacts_list",
		core.ToolDescription("List all contacts", `{"limit":25}`),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 50)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleContactsList(src, ctx, req)
	})
}

func handleContactsSearch(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, errResult := core.RequireStringArgument(req, "query", `{"query":"Alice Smith","limit":5}`)
	if errResult != nil {
		return errResult, nil
	}
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT c.resource_name, c.display_name, c.emails, c.phones, c.organizations, c.updated_time
		FROM contacts_people_fts
		JOIN contacts_people c ON c.rowid = contacts_people_fts.rowid
		WHERE contacts_people_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var rn, name, emails, phones, orgs, updated string
		if err := rows.Scan(&rn, &name, &emails, &phones, &orgs, &updated); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"resource_name": rn, "display_name": name, "emails": emails,
			"phones": phones, "organizations": orgs, "updated_time": updated,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

func handleContactsList(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 50)
	if src.db == nil {
		return mcp.NewToolResultText("{\"contacts\":[],\"count\":0}"), nil
	}
	rows, err := src.db.Query(`SELECT resource_name, display_name, emails, phones, organizations, updated_time
		FROM contacts_people ORDER BY display_name ASC LIMIT ?`, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list contacts: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var rn, name, emails, phones, orgs, updated string
		if err := rows.Scan(&rn, &name, &emails, &phones, &orgs, &updated); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"resource_name": rn, "display_name": name, "emails": emails,
			"phones": phones, "organizations": orgs, "updated_time": updated,
		})
	}
	return core.JsonResult(map[string]interface{}{"contacts": results, "count": len(results)})
}

func contactsSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT resource_name, display_name, emails, phones, organizations, notes, updated_time
		FROM contacts_people`)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()
	var entries []core.SearchEntry
	for rows.Next() {
		var rn, name, emails, phones, orgs, notes, updated string
		if err := rows.Scan(&rn, &name, &emails, &phones, &orgs, &notes, &updated); err != nil { // nocov
			continue
		}
		content := name
		if emails != "" {
			content += " " + emails
		}
		if phones != "" {
			content += " " + phones
		}
		if orgs != "" {
			content += " " + orgs
		}
		meta, _ := json.Marshal(map[string]interface{}{
			"resource_name": rn, "emails": emails, "phones": phones, "updated_time": updated,
		})
		entries = append(entries, core.SearchEntry{
			Source: sourceName, SourceID: rn, ContentType: "contact",
			Title: name, Content: content, Metadata: meta,
		})
	}
	return entries, nil
}
