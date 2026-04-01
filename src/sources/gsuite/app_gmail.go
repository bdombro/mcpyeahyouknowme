package gsuite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

var gmailAppDef = &appDef{
	name:          "gmail",
	displayName:   "Gmail",
	initSchema:    initGmailSchema,
	syncFunc:      syncGmail,
	registerTools: registerGmailTools,
	searchEntries: gmailSearchEntries,
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "gmail_messages") },
	tablesToDrop:  []string{"gmail_messages", "gmail_messages_fts"},
}

func initGmailSchema(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS gmail_messages (
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
	);
	`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS gmail_messages_fts USING fts5(
		subject, body_text, from_addr, to_addrs, folder,
		content='gmail_messages',
		content_rowid='rowid'
	);
	CREATE TRIGGER IF NOT EXISTS gmail_messages_ai AFTER INSERT ON gmail_messages BEGIN
		INSERT INTO gmail_messages_fts(rowid, subject, body_text, from_addr, to_addrs, folder)
		VALUES (new.rowid, new.subject, new.body_text, new.from_addr, new.to_addrs, new.folder);
	END;
	CREATE TRIGGER IF NOT EXISTS gmail_messages_ad AFTER DELETE ON gmail_messages BEGIN
		DELETE FROM gmail_messages_fts WHERE rowid = old.rowid;
	END;
	CREATE TRIGGER IF NOT EXISTS gmail_messages_au AFTER UPDATE ON gmail_messages BEGIN
		INSERT INTO gmail_messages_fts(gmail_messages_fts, rowid, subject, body_text, from_addr, to_addrs, folder)
		VALUES('delete', old.rowid, old.subject, old.body_text, old.from_addr, old.to_addrs, old.folder);
		INSERT INTO gmail_messages_fts(rowid, subject, body_text, from_addr, to_addrs, folder)
		VALUES (new.rowid, new.subject, new.body_text, new.from_addr, new.to_addrs, new.folder);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	db.Exec("INSERT INTO gmail_messages_fts(gmail_messages_fts) VALUES('rebuild')")
	return nil
}

func syncGmail(sctx syncContext) error { // nocov
	ctx := sctx.Ctx.(context.Context)
	sctx.SetStatus("syncing")
	defer sctx.SetStatus("idle")

	gmailService, err := gmail.NewService(ctx, option.WithHTTPClient(sctx.HTTPClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Gmail service: %w", err)
	}

	// List all message IDs excluding trash
	remoteIDs := make(map[string]bool)
	var totalFetched int
	pageToken := ""
	for {
		call := gmailService.Users.Messages.List("me").Q("-in:trash").MaxResults(500)
		if pageToken != "" { // nocov
			call = call.PageToken(pageToken)
		}
		res, err := call.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list messages: %w", err)
		}

		for _, msg := range res.Messages {
			remoteIDs[msg.Id] = true
			var exists int
			sctx.DB.QueryRow("SELECT 1 FROM gmail_messages WHERE id = ?", msg.Id).Scan(&exists)
			if exists == 1 {
				continue
			}
			full, err := gmailService.Users.Messages.Get("me", msg.Id).Format("full").Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to fetch message %s: %v\n", msg.Id, err)
				continue
			}
			storeGmailMessage(sctx.DB, full)
			totalFetched++
			sctx.SetStatus(fmt.Sprintf("syncing:%d", totalFetched))
		}

		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}

	// Remove messages that went to trash
	deleteOrphanedRows(sctx.DB, "gmail_messages", remoteIDs)
	fmt.Printf("Gmail sync: %d new messages fetched\n", totalFetched)
	return nil
}

func storeGmailMessage(db *sql.DB, msg *gmail.Message) {
	headers := parseGmailHeaders(msg)
	body := extractGmailBody(msg.Payload)
	labels := strings.Join(msg.LabelIds, ",")
	folder := primaryFolder(msg.LabelIds)
	hasAttachments := 0
	if hasGmailAttachments(msg.Payload) {
		hasAttachments = 1
	}
	db.Exec(`INSERT OR REPLACE INTO gmail_messages
		(id, thread_id, labels, folder, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		 date, snippet, body_text, has_attachments, size_estimate, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		msg.Id, msg.ThreadId, labels, folder,
		headers["Subject"], headers["From"], headers["To"], headers["Cc"], headers["Bcc"],
		headers["Date"], msg.Snippet, body, hasAttachments, msg.SizeEstimate)
}

func parseGmailHeaders(msg *gmail.Message) map[string]string {
	h := make(map[string]string)
	if msg.Payload == nil {
		return h
	}
	for _, hdr := range msg.Payload.Headers {
		switch hdr.Name {
		case "Subject", "From", "To", "Cc", "Bcc", "Date":
			h[hdr.Name] = hdr.Value
		}
	}
	return h
}

func extractGmailBody(payload *gmail.MessagePart) string {
	if payload == nil {
		return ""
	}
	// Prefer text/plain
	if payload.MimeType == "text/plain" && payload.Body != nil && payload.Body.Data != "" {
		data, err := base64.URLEncoding.DecodeString(payload.Body.Data)
		if err == nil {
			return string(data)
		}
	}
	// Recurse into parts
	for _, part := range payload.Parts {
		if part.MimeType == "text/plain" && part.Body != nil && part.Body.Data != "" {
			data, err := base64.URLEncoding.DecodeString(part.Body.Data)
			if err == nil {
				return string(data)
			}
		}
	}
	// Fall back to text/html stripped
	if payload.MimeType == "text/html" && payload.Body != nil && payload.Body.Data != "" {
		data, err := base64.URLEncoding.DecodeString(payload.Body.Data)
		if err == nil {
			return stripHTML(string(data))
		}
	}
	for _, part := range payload.Parts {
		if part.MimeType == "text/html" && part.Body != nil && part.Body.Data != "" {
			data, err := base64.URLEncoding.DecodeString(part.Body.Data)
			if err == nil {
				return stripHTML(string(data))
			}
		}
		// Recurse deeper (multipart/alternative inside multipart/mixed)
		result := extractGmailBody(part)
		if result != "" {
			return result
		}
	}
	return ""
}

// stripHTML does a basic HTML tag removal. Good enough for search indexing.
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasGmailAttachments(payload *gmail.MessagePart) bool {
	if payload == nil {
		return false
	}
	for _, part := range payload.Parts {
		if part.Filename != "" && part.Body != nil && part.Body.AttachmentId != "" {
			return true
		}
		if hasGmailAttachments(part) {
			return true
		}
	}
	return false
}

// metaLabels are Gmail system labels that don't represent a mailbox folder.
var metaLabels = map[string]bool{
	"UNREAD": true, "STARRED": true, "IMPORTANT": true,
}

func primaryFolder(labelIDs []string) string {
	priority := []string{"INBOX", "SENT", "DRAFT", "SPAM"}
	labelSet := make(map[string]bool, len(labelIDs))
	for _, l := range labelIDs {
		labelSet[l] = true
	}
	for _, p := range priority {
		if labelSet[p] {
			return p
		}
	}
	// Use first non-meta label (e.g. custom label or CATEGORY_*)
	for _, l := range labelIDs {
		if !metaLabels[l] {
			return l
		}
	}
	return "ARCHIVE"
}

func registerGmailTools(src *Source, prefix string, s toolAdder) {
	s.AddTool(mcp.NewTool(prefix+"gmail_search",
		mcp.WithDescription("Search across all Gmail messages (excluding trash)"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailSearch(src, ctx, req)
	})
	s.AddTool(mcp.NewTool(prefix+"gmail_get_message",
		mcp.WithDescription("Get full content of a specific Gmail message by ID"),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Gmail message ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailGetMessage(src, ctx, req)
	})
	s.AddTool(mcp.NewTool(prefix+"gmail_list_recent",
		mcp.WithDescription("List recent Gmail messages"),
		mcp.WithString("folder", mcp.Description("Filter by folder (INBOX, SENT, DRAFT, etc.)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailListRecent(src, ctx, req)
	})
	s.AddTool(mcp.NewTool(prefix+"gmail_download_attachment",
		mcp.WithDescription("Download a Gmail attachment on-demand (not cached locally)"),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Gmail message ID")),
		mcp.WithString("attachment_id", mcp.Required(), mcp.Description("Attachment ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailDownloadAttachment(src, ctx, req)
	})
}

func handleGmailSearch(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT m.id, m.subject, m.from_addr, m.to_addrs, m.date, m.folder, m.snippet, m.has_attachments,
		       snippet(gmail_messages_fts, 1, '<mark>', '</mark>', '...', 32) as match_snippet
		FROM gmail_messages_fts
		JOIN gmail_messages m ON m.rowid = gmail_messages_fts.rowid
		WHERE gmail_messages_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, subject, from, to, date, folder, snippet string
		var hasAttach int
		var matchSnippet string
		if err := rows.Scan(&id, &subject, &from, &to, &date, &folder, &snippet, &hasAttach, &matchSnippet); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "subject": subject, "from": from, "to": to,
			"date": date, "folder": folder, "snippet": matchSnippet,
			"has_attachments": hasAttach > 0,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

func handleGmailGetMessage(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	msgID, _ := req.RequireString("message_id")
	if src.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}
	var subject, from, to, cc, bcc, date, folder, body, labels string
	var hasAttach, sizeEst int
	err := src.db.QueryRow(`SELECT subject, from_addr, to_addrs, cc_addrs, bcc_addrs, date, folder,
		body_text, labels, has_attachments, size_estimate FROM gmail_messages WHERE id = ?`, msgID).
		Scan(&subject, &from, &to, &cc, &bcc, &date, &folder, &body, &labels, &hasAttach, &sizeEst)
	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Message not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve message: %v", err)), nil
	}
	return core.JsonResult(map[string]interface{}{
		"id": msgID, "subject": subject, "from": from, "to": to, "cc": cc, "bcc": bcc,
		"date": date, "folder": folder, "body": body, "labels": labels,
		"has_attachments": hasAttach > 0, "size_estimate": sizeEst,
	})
}

func handleGmailListRecent(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := core.IntArg(req.GetArguments(), "limit", 20)
	folder, _ := req.GetArguments()["folder"].(string)
	if src.db == nil {
		return mcp.NewToolResultText("{\"messages\":[],\"count\":0}"), nil
	}
	var rows *sql.Rows
	var err error
	if folder != "" {
		rows, err = src.db.Query(`SELECT id, subject, from_addr, to_addrs, date, folder, snippet, has_attachments
			FROM gmail_messages WHERE folder = ? ORDER BY date DESC LIMIT ?`, folder, limit)
	} else {
		rows, err = src.db.Query(`SELECT id, subject, from_addr, to_addrs, date, folder, snippet, has_attachments
			FROM gmail_messages ORDER BY date DESC LIMIT ?`, limit)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list messages: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, subject, from, to, date, fldr, snippet string
		var hasAttach int
		if err := rows.Scan(&id, &subject, &from, &to, &date, &fldr, &snippet, &hasAttach); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "subject": subject, "from": from, "to": to,
			"date": date, "folder": fldr, "snippet": snippet,
			"has_attachments": hasAttach > 0,
		})
	}
	return core.JsonResult(map[string]interface{}{"messages": results, "count": len(results)})
}

// handleGmailDownloadAttachment fetches an attachment on-demand via the Gmail API.
// This is an intentional exception to the "reads from local DB only" rule.
func handleGmailDownloadAttachment(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	msgID, _ := req.RequireString("message_id")
	attachID, _ := req.RequireString("attachment_id")
	if !src.isAuthenticated() {
		return mcp.NewToolResultError("Not authenticated — run 'mcpyeahyouknowme gsuite login'"), nil
	}
	oauthConfig := src.getOAuthConfig()
	httpClient := oauthConfig.Client(ctx, src.token)
	gmailSvc, err := gmail.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create Gmail service: %v", err)), nil
	}
	att, err := gmailSvc.Users.Messages.Attachments.Get("me", msgID, attachID).Do()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to download attachment: %v", err)), nil
	}
	return core.JsonResult(map[string]interface{}{
		"message_id":    msgID,
		"attachment_id": attachID,
		"size":          att.Size,
		"data_base64":   att.Data,
	})
}

func gmailSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	rows, err := db.Query(`SELECT id, subject, from_addr, to_addrs, cc_addrs, bcc_addrs, date, folder, body_text FROM gmail_messages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []core.SearchEntry
	for rows.Next() {
		var id, subject, from, to, cc, bcc, date, folder, body string
		if err := rows.Scan(&id, &subject, &from, &to, &cc, &bcc, &date, &folder, &body); err != nil { // nocov
			continue
		}
		participants := from
		if to != "" {
			participants += ", " + to
		}

		meta, _ := json.Marshal(map[string]interface{}{
			"message_id": id, "from": from, "to": to, "cc": cc, "bcc": bcc,
			"date": date, "folder": folder,
		})
		entries = append(entries, core.SearchEntry{
			Source: sourceName, SourceID: id, ContentType: "email_subject",
			Title: subject, Content: subject + " — " + participants, Metadata: meta,
		})
		if len(body) > 0 {
			chunkSize := 5000
			for i := 0; i < len(body); i += chunkSize {
				end := i + chunkSize
				if end > len(body) {
					end = len(body)
				}
				chunkMeta, _ := json.Marshal(map[string]interface{}{
					"message_id": id, "subject": subject, "from": from,
					"date": date, "folder": folder, "chunk_index": i / chunkSize,
				})
				entries = append(entries, core.SearchEntry{
					Source: sourceName, SourceID: id, ContentType: "email_content",
					Title: subject, Content: body[i:end], Metadata: chunkMeta,
				})
			}
		}
	}
	return entries, nil
}
