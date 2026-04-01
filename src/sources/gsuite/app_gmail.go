package gsuite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"

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
	countRows:     func(db *sql.DB) (int, error) { return countTable(db, "gmail_messages") }, // nocov
	tablesToDrop:  []string{"gmail_threads", "gmail_messages", "gmail_messages_fts"},
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
		body_raw TEXT NOT NULL DEFAULT '',
		body_visible TEXT NOT NULL DEFAULT '',
		has_attachments INTEGER NOT NULL DEFAULT 0,
		size_estimate INTEGER NOT NULL DEFAULT 0,
		last_synced TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS gmail_threads (
		thread_id TEXT PRIMARY KEY,
		subject TEXT NOT NULL DEFAULT '',
		participants TEXT NOT NULL DEFAULT '',
		message_count INTEGER NOT NULL DEFAULT 0,
		first_date TEXT NOT NULL DEFAULT '',
		last_date TEXT NOT NULL DEFAULT '',
		last_message_id TEXT NOT NULL DEFAULT '',
		thread_text_visible TEXT NOT NULL DEFAULT '',
		last_synced TEXT NOT NULL
	);
	`)
	if err != nil { // nocov
		return err
	}
	migrated := false
	if added, err := addGmailMessageColumnIfMissing(db, "body_raw", "TEXT NOT NULL DEFAULT ''"); err != nil { // nocov
		return err
	} else if added {
		migrated = true
	}
	if added, err := addGmailMessageColumnIfMissing(db, "body_visible", "TEXT NOT NULL DEFAULT ''"); err != nil { // nocov
		return err
	} else if added {
		migrated = true
	}
	if migrated {
		if err := backfillGmailMessageBodies(db); err != nil { // nocov
			return err
		}
	}
	if migrated || !gmailFTSIndexesBodyVisible(db) {
		if err := recreateGmailMessageFTS(db); err != nil { // nocov
			return err
		}
	}
	if migrated || !gmailThreadsPopulated(db) {
		if err := rebuildAllGmailThreads(db); err != nil { // nocov
			return err
		}
	}
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
	if err := rebuildAllGmailThreads(sctx.DB); err != nil {
		return fmt.Errorf("failed to rebuild gmail threads: %w", err)
	}
	fmt.Printf("Gmail sync: %d new messages fetched\n", totalFetched)
	return nil
}

// gmailStoredRecord holds column values for upserting a synced Gmail message row.
type gmailStoredRecord struct {
	ID             string
	ThreadID       string
	Labels         string
	Folder         string
	Subject        string
	FromAddr       string
	ToAddrs        string
	CcAddrs        string
	BccAddrs       string
	Date           string
	Snippet        string
	BodyText       string
	BodyRaw        string
	BodyVisible    string
	HasAttachments int
	SizeEstimate   int64
}

func buildGmailStoredRecord(msg *gmail.Message) gmailStoredRecord {
	if msg == nil {
		return gmailStoredRecord{}
	}
	headers := parseGmailHeaders(msg)
	bodyRaw := extractGmailBody(msg.Payload)
	bodyVisible := deriveVisibleBody(bodyRaw)
	labels := strings.Join(msg.LabelIds, ",")
	folder := primaryFolder(msg.LabelIds)
	hasAttachments := 0
	if hasGmailAttachments(msg.Payload) {
		hasAttachments = 1
	}
	return gmailStoredRecord{
		ID:             msg.Id,
		ThreadID:       msg.ThreadId,
		Labels:         labels,
		Folder:         folder,
		Subject:        headers["Subject"],
		FromAddr:       headers["From"],
		ToAddrs:        headers["To"],
		CcAddrs:        headers["Cc"],
		BccAddrs:       headers["Bcc"],
		Date:           headers["Date"],
		Snippet:        msg.Snippet,
		BodyText:       bodyRaw,
		BodyRaw:        bodyRaw,
		BodyVisible:    bodyVisible,
		HasAttachments: hasAttachments,
		SizeEstimate:   msg.SizeEstimate,
	}
}

func storeGmailMessage(db *sql.DB, msg *gmail.Message) {
	rec := buildGmailStoredRecord(msg)
	// body_text is a legacy alias kept for backward compatibility; body_raw is canonical.
	db.Exec(`INSERT OR REPLACE INTO gmail_messages
		(id, thread_id, labels, folder, subject, from_addr, to_addrs, cc_addrs, bcc_addrs,
		 date, snippet, body_text, body_raw, body_visible, has_attachments, size_estimate, last_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		rec.ID, rec.ThreadID, rec.Labels, rec.Folder,
		rec.Subject, rec.FromAddr, rec.ToAddrs, rec.CcAddrs, rec.BccAddrs,
		rec.Date, rec.Snippet, rec.BodyText, rec.BodyRaw, rec.BodyVisible, rec.HasAttachments, rec.SizeEstimate)
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

var (
	replyBoundaryRe       = regexp.MustCompile(`(?i)^on .+wrote:\s*$`)
	forwardedBoundaryRe   = regexp.MustCompile(`(?i)^(begin forwarded message:|[- ]*original message[- ]*|[- ]*forwarded message[- ]*)$`)
	headerLineRe          = regexp.MustCompile(`(?i)^(from|sent|to|subject|date|cc|bcc):`)
	mobileSignatureLineRe = regexp.MustCompile(`(?i)^sent from my (iphone|ipad|android|mobile device)$`)
)

func deriveVisibleBody(raw string) string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}
	visible := stripQuotedReplyBlocks(normalized)
	if shouldFallbackToRaw(normalized, visible) {
		return normalized
	}
	return visible
}

func stripQuotedReplyBlocks(body string) string {
	lines := strings.Split(body, "\n")
	cut := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isStrongReplyBoundary(trimmed) && hasAuthoredContent(lines[:i]) {
			cut = i
			break
		}
		if isQuotedHeaderBlock(lines, i) && hasAuthoredContent(lines[:i]) {
			cut = i
			break
		}
	}
	lines = trimTrailingQuotedBlock(lines[:cut])
	lines = trimTrailingMobileSignature(lines)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isStrongReplyBoundary(line string) bool {
	return replyBoundaryRe.MatchString(line) || forwardedBoundaryRe.MatchString(line)
}

func isQuotedHeaderBlock(lines []string, start int) bool {
	matched := 0
	for i := start; i < len(lines) && i < start+5; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			if matched > 0 {
				break
			}
			continue
		}
		if headerLineRe.MatchString(trimmed) {
			matched++
			continue
		}
		break
	}
	return matched >= 2
}

func hasAuthoredContent(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, ">") {
			return true
		}
	}
	return false
}

func trimTrailingQuotedBlock(lines []string) []string {
	i := len(lines) - 1
	sawQuoted := false
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		switch {
		case trimmed == "":
			i--
		case strings.HasPrefix(trimmed, ">"):
			sawQuoted = true
			i--
		default:
			if sawQuoted && hasAuthoredContent(lines[:i+1]) {
				return trimTrailingBlankLines(lines[:i+1])
			}
			return trimTrailingBlankLines(lines)
		}
	}
	if sawQuoted {
		return []string{}
	}
	return trimTrailingBlankLines(lines)
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

func trimTrailingMobileSignature(lines []string) []string {
	lines = trimTrailingBlankLines(lines)
	if len(lines) < 2 {
		return lines
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !mobileSignatureLineRe.MatchString(last) {
		return lines
	}
	if strings.TrimSpace(lines[len(lines)-2]) != "" {
		return lines
	}
	return trimTrailingBlankLines(lines[:len(lines)-2])
}

func shouldFallbackToRaw(raw, visible string) bool {
	raw = strings.TrimSpace(raw)
	visible = strings.TrimSpace(visible)
	if raw == "" {
		return false
	}
	if visible == "" {
		return true
	}
	if len(raw) >= 200 && len(visible) < 20 {
		return true
	}
	return len(raw) >= 500 && len(visible)*10 < len(raw)
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
	s.AddTool(core.NewReadOnlyTool(prefix+"gmail_search",
		core.ToolDescription("Search across all Gmail messages (excluding trash)", `{"query":"invoice overdue","limit":5}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 10)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailSearch(src, ctx, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"gmail_get_message",
		core.ToolDescription("Get full content of a specific Gmail message by ID", `{"message_id":"190a2b3c4d"}`),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Gmail message ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailGetMessage(src, ctx, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"gmail_get_thread",
		core.ToolDescription("Get a reconstructed Gmail thread by thread ID", `{"thread_id":"190a2b3c4d","include_raw":false}`),
		mcp.WithString("thread_id", mcp.Required(), mcp.Description("Gmail thread ID")),
		mcp.WithBoolean("include_raw", mcp.Description("Include raw unstripped message bodies")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailGetThread(src, ctx, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"gmail_list_recent",
		core.ToolDescription("List recent Gmail messages", `{"folder":"INBOX","limit":10}`),
		mcp.WithString("folder", mcp.Description("Filter by folder (INBOX, SENT, DRAFT, etc.)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailListRecent(src, ctx, req)
	})
	s.AddTool(core.NewReadOnlyTool(prefix+"gmail_download_attachment",
		core.ToolDescription("Download a Gmail attachment on-demand (not cached locally)", `{"message_id":"190a2b3c4d","attachment_id":"ANGjdJ..."}`),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Gmail message ID")),
		mcp.WithString("attachment_id", mcp.Required(), mcp.Description("Attachment ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
		return handleGmailDownloadAttachment(src, ctx, req)
	})
}

func handleGmailSearch(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, errResult := core.RequireStringArgument(req, "query", `{"query":"invoice overdue","limit":5}`)
	if errResult != nil {
		return errResult, nil
	}
	limit := core.IntArg(req.GetArguments(), "limit", 10)
	if src.db == nil {
		return mcp.NewToolResultText("[]"), nil
	}
	rows, err := src.db.Query(`
		SELECT m.id, m.thread_id, m.subject, m.from_addr, m.to_addrs, m.date, m.folder, m.snippet, m.has_attachments,
		       snippet(gmail_messages_fts, 1, '<mark>', '</mark>', '...', 32) as match_snippet
		FROM gmail_messages_fts
		JOIN gmail_messages m ON m.rowid = gmail_messages_fts.rowid
		WHERE gmail_messages_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, threadID, subject, from, to, date, folder, snippet string
		var hasAttach int
		var matchSnippet string
		if err := rows.Scan(&id, &threadID, &subject, &from, &to, &date, &folder, &snippet, &hasAttach, &matchSnippet); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "thread_id": threadID, "subject": subject, "from": from, "to": to,
			"date": date, "folder": folder, "snippet": matchSnippet,
			"has_attachments": hasAttach > 0,
		})
	}
	return core.JsonResult(map[string]interface{}{"query": query, "results": results, "count": len(results)})
}

func handleGmailGetMessage(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	msgID, errResult := core.RequireStringArgument(req, "message_id", `{"message_id":"190a2b3c4d"}`)
	if errResult != nil {
		return errResult, nil
	}
	if src.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}
	var threadID, subject, from, to, cc, bcc, date, folder, bodyRaw, labels string
	var hasAttach, sizeEst int
	err := src.db.QueryRow(`SELECT thread_id, subject, from_addr, to_addrs, cc_addrs, bcc_addrs, date, folder,
		COALESCE(NULLIF(body_raw, ''), body_text), labels, has_attachments, size_estimate
		FROM gmail_messages WHERE id = ?`, msgID).
		Scan(&threadID, &subject, &from, &to, &cc, &bcc, &date, &folder, &bodyRaw, &labels, &hasAttach, &sizeEst)
	if err == sql.ErrNoRows {
		return mcp.NewToolResultError("Message not found"), nil
	}
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve message: %v", err)), nil
	}
	return core.JsonResult(map[string]interface{}{
		"id": msgID, "thread_id": threadID, "subject": subject, "from": from, "to": to, "cc": cc, "bcc": bcc,
		"date": date, "folder": folder, "body": bodyRaw, "labels": labels,
		"has_attachments": hasAttach > 0, "size_estimate": sizeEst,
	})
}

func handleGmailGetThread(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	threadID, errResult := core.RequireStringArgument(req, "thread_id", `{"thread_id":"190a2b3c4d","include_raw":false}`)
	if errResult != nil {
		return errResult, nil
	}
	includeRaw := core.BoolArg(req.GetArguments(), "include_raw", false)
	if src.db == nil {
		return mcp.NewToolResultError("Database not available"), nil
	}
	messages, err := loadGmailMessagesByThread(src.db, threadID)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve thread: %v", err)), nil
	}
	if len(messages) == 0 {
		return mcp.NewToolResultError("Thread not found"), nil
	}
	meta, err := loadGmailThreadMeta(src.db, threadID)
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve thread metadata: %v", err)), nil
	}
	if meta.threadID == "" {
		meta = buildThreadRecord(messages)
	}
	resultMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		body := msg.BodyVisible
		if body == "" { // nocov — scanGmailMessages already derives BodyVisible from BodyRaw
			body = msg.BodyRaw
		}
		entry := map[string]interface{}{
			"message_id":      msg.ID,
			"from":            msg.From,
			"to":              msg.To,
			"cc":              msg.CC,
			"bcc":             msg.BCC,
			"date":            msg.Date,
			"folder":          msg.Folder,
			"labels":          msg.Labels,
			"has_attachments": msg.HasAttachments,
			"body":            body,
		}
		if includeRaw {
			entry["body_raw"] = msg.BodyRaw
		}
		resultMessages = append(resultMessages, entry)
	}
	return core.JsonResult(map[string]interface{}{
		"thread_id":     meta.threadID,
		"subject":       meta.Subject,
		"participants":  meta.Participants,
		"message_count": meta.MessageCount,
		"first_date":    meta.FirstDate,
		"last_date":     meta.LastDate,
		"messages":      resultMessages,
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
		rows, err = src.db.Query(`SELECT id, thread_id, subject, from_addr, to_addrs, date, folder, snippet, has_attachments
			FROM gmail_messages WHERE folder = ? ORDER BY date DESC LIMIT ?`, folder, limit)
	} else {
		rows, err = src.db.Query(`SELECT id, thread_id, subject, from_addr, to_addrs, date, folder, snippet, has_attachments
			FROM gmail_messages ORDER BY date DESC LIMIT ?`, limit)
	}
	if err != nil { // nocov
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list messages: %v", err)), nil
	}
	defer rows.Close()
	var results []map[string]interface{}
	for rows.Next() {
		var id, threadID, subject, from, to, date, fldr, snippet string
		var hasAttach int
		if err := rows.Scan(&id, &threadID, &subject, &from, &to, &date, &fldr, &snippet, &hasAttach); err != nil { // nocov
			continue
		}
		results = append(results, map[string]interface{}{
			"id": id, "thread_id": threadID, "subject": subject, "from": from, "to": to,
			"date": date, "folder": fldr, "snippet": snippet,
			"has_attachments": hasAttach > 0,
		})
	}
	return core.JsonResult(map[string]interface{}{"messages": results, "count": len(results)})
}

// handleGmailDownloadAttachment fetches an attachment on-demand via the Gmail API.
// This is an intentional exception to the "reads from local DB only" rule.
func handleGmailDownloadAttachment(src *Source, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) { // nocov
	msgID, errResult := core.RequireStringArgument(req, "message_id", `{"message_id":"190a2b3c4d","attachment_id":"ANGjdJ..."}`)
	if errResult != nil {
		return errResult, nil
	}
	attachID, errResult := core.RequireStringArgument(req, "attachment_id", `{"message_id":"190a2b3c4d","attachment_id":"ANGjdJ..."}`)
	if errResult != nil {
		return errResult, nil
	}
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

// gmailThreadSearchSummary is one row from gmail_threads used when building global search entries.
type gmailThreadSearchSummary struct {
	threadID     string
	subject      string
	participants string
	messageCount int
	firstDate    string
	lastDate     string
}

func gmailSearchEntriesForThread(sourceName string, summary gmailThreadSearchSummary, messages []gmailMessageRecord) []core.SearchEntry {
	meta, _ := json.Marshal(map[string]interface{}{
		"thread_id":     summary.threadID,
		"message_count": summary.messageCount,
		"first_date":    summary.firstDate,
		"last_date":     summary.lastDate,
		"participants":  summary.participants,
	})
	var entries []core.SearchEntry
	if summary.subject != "" {
		entries = append(entries, core.SearchEntry{
			Source: sourceName, SourceID: summary.threadID, ContentType: "email_thread_subject",
			Title: summary.subject, Content: summary.subject, Metadata: meta,
		})
	}
	if summary.participants != "" {
		entries = append(entries, core.SearchEntry{
			Source: sourceName, SourceID: summary.threadID, ContentType: "email_thread_participants",
			Title: summary.subject, Content: summary.participants, Metadata: meta,
		})
	}
	chunks := buildGmailThreadChunks(summary.subject, summary.participants, messages)
	for i, chunk := range chunks {
		chunkMeta, _ := json.Marshal(map[string]interface{}{
			"thread_id":        summary.threadID,
			"subject":          summary.subject,
			"participants":     summary.participants,
			"chunk_index":      i,
			"start_message_id": chunk.StartMessageID,
			"end_message_id":   chunk.EndMessageID,
			"start_date":       chunk.StartDate,
			"end_date":         chunk.EndDate,
		})
		entries = append(entries, core.SearchEntry{
			Source:      sourceName,
			SourceID:    fmt.Sprintf("%s#chunk:%03d", summary.threadID, i),
			ContentType: "email_thread_content",
			Title:       summary.subject,
			Content:     chunk.Content,
			Metadata:    chunkMeta,
		})
	}
	return entries
}

func gmailSearchEntries(db *sql.DB, sourceName string) ([]core.SearchEntry, error) {
	threadRows, err := db.Query(`SELECT thread_id, subject, participants, message_count, first_date, last_date
		FROM gmail_threads ORDER BY last_date DESC, thread_id`)
	if err != nil { // nocov
		return nil, err
	}
	var summaries []gmailThreadSearchSummary
	for threadRows.Next() {
		var summary gmailThreadSearchSummary
		if err := threadRows.Scan(&summary.threadID, &summary.subject, &summary.participants, &summary.messageCount, &summary.firstDate, &summary.lastDate); err != nil { // nocov
			continue
		}
		summaries = append(summaries, summary)
	}
	threadRows.Close()
	if err := threadRows.Err(); err != nil { // nocov
		return nil, err
	}
	var entries []core.SearchEntry
	for _, summary := range summaries {
		messages, err := loadGmailMessagesByThread(db, summary.threadID)
		if err != nil { // nocov
			continue
		}
		entries = append(entries, gmailSearchEntriesForThread(sourceName, summary, messages)...)
	}
	return entries, nil
}

type gmailMessageRecord struct {
	ID             string
	ThreadID       string
	Subject        string
	From           string
	To             string
	CC             string
	BCC            string
	Date           string
	Folder         string
	Labels         string
	BodyRaw        string
	BodyVisible    string
	HasAttachments bool
}

type gmailThreadRecord struct {
	threadID          string
	Subject           string
	Participants      string
	MessageCount      int
	FirstDate         string
	LastDate          string
	LastMessageID     string
	ThreadTextVisible string
}

type gmailThreadChunk struct {
	Content        string
	StartMessageID string
	EndMessageID   string
	StartDate      string
	EndDate        string
}

// addGmailMessageColumnIfMissing adds a column if it does not exist and reports
// whether the ALTER actually ran (true = column was missing and has been added).
func addGmailMessageColumnIfMissing(db *sql.DB, column, definition string) (bool, error) {
	exists, err := tableHasColumn(db, "gmail_messages", column)
	if err != nil { // nocov
		return false, err
	}
	if exists {
		return false, nil
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE gmail_messages ADD COLUMN %s %s", column, definition))
	return err == nil, err
}

// gmailFTSIndexesBodyVisible returns true when the insert trigger already
// references body_visible, meaning the FTS schema is up-to-date.
func gmailFTSIndexesBodyVisible(db *sql.DB) bool {
	var triggerSQL sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name='gmail_messages_ai'`).Scan(&triggerSQL)
	if err != nil || !triggerSQL.Valid {
		return false
	}
	return strings.Contains(triggerSQL.String, "body_visible")
}

// gmailThreadsPopulated reports whether the gmail_threads table has any rows.
func gmailThreadsPopulated(db *sql.DB) bool {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gmail_threads`).Scan(&count); err != nil { // nocov
		return false
	}
	return count > 0
}

func tableHasColumn(db *sql.DB, tableName, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil { // nocov
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil { // nocov
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// mergeGmailMessageBodyFields computes canonical body_text / body_raw / body_visible for a row
// and reports whether an UPDATE is needed.
func mergeGmailMessageBodyFields(bodyText, bodyRaw, bodyVisible string) (nextBodyText, nextRaw, nextVisible string, needsUpdate bool) {
	nextRaw = bodyRaw
	if nextRaw == "" {
		nextRaw = bodyText
	}
	nextVisible = bodyVisible
	if nextVisible == "" {
		nextVisible = deriveVisibleBody(nextRaw)
	}
	nextBodyText = bodyText
	if nextBodyText == "" {
		nextBodyText = nextRaw
	}
	needsUpdate = nextBodyText != bodyText || nextRaw != bodyRaw || nextVisible != bodyVisible
	return nextBodyText, nextRaw, nextVisible, needsUpdate
}

func backfillGmailMessageBodies(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, body_text, body_raw, body_visible FROM gmail_messages`)
	if err != nil { // nocov
		return err
	}
	defer rows.Close()
	type pendingUpdate struct {
		id          string
		bodyText    string
		bodyRaw     string
		bodyVisible string
	}
	var updates []pendingUpdate
	for rows.Next() {
		var id, bodyText, bodyRaw, bodyVisible string
		if err := rows.Scan(&id, &bodyText, &bodyRaw, &bodyVisible); err != nil { // nocov
			return err
		}
		nextBodyText, nextRaw, nextVisible, changed := mergeGmailMessageBodyFields(bodyText, bodyRaw, bodyVisible)
		if changed {
			updates = append(updates, pendingUpdate{
				id:          id,
				bodyText:    nextBodyText,
				bodyRaw:     nextRaw,
				bodyVisible: nextVisible,
			})
		}
	}
	if err := rows.Err(); err != nil { // nocov
		return err
	}
	for _, update := range updates {
		if _, err := db.Exec(`UPDATE gmail_messages SET body_text = ?, body_raw = ?, body_visible = ? WHERE id = ?`,
			update.bodyText, update.bodyRaw, update.bodyVisible, update.id); err != nil { // nocov
			return err
		}
	}
	return nil
}

func recreateGmailMessageFTS(db *sql.DB) error {
	_, err := db.Exec(`
	DROP TRIGGER IF EXISTS gmail_messages_ai;
	DROP TRIGGER IF EXISTS gmail_messages_ad;
	DROP TRIGGER IF EXISTS gmail_messages_au;
	DROP TABLE IF EXISTS gmail_messages_fts;
	CREATE VIRTUAL TABLE gmail_messages_fts USING fts5(
		subject, body_visible, from_addr, to_addrs, folder,
		content='gmail_messages',
		content_rowid='rowid'
	);
	CREATE TRIGGER gmail_messages_ai AFTER INSERT ON gmail_messages BEGIN
		INSERT INTO gmail_messages_fts(rowid, subject, body_visible, from_addr, to_addrs, folder)
		VALUES (new.rowid, new.subject, new.body_visible, new.from_addr, new.to_addrs, new.folder);
	END;
	CREATE TRIGGER gmail_messages_ad AFTER DELETE ON gmail_messages BEGIN
		DELETE FROM gmail_messages_fts WHERE rowid = old.rowid;
	END;
	CREATE TRIGGER gmail_messages_au AFTER UPDATE ON gmail_messages BEGIN
		INSERT INTO gmail_messages_fts(gmail_messages_fts, rowid, subject, body_visible, from_addr, to_addrs, folder)
		VALUES('delete', old.rowid, old.subject, old.body_visible, old.from_addr, old.to_addrs, old.folder);
		INSERT INTO gmail_messages_fts(rowid, subject, body_visible, from_addr, to_addrs, folder)
		VALUES (new.rowid, new.subject, new.body_visible, new.from_addr, new.to_addrs, new.folder);
	END;
	`)
	if err != nil { // nocov
		return err
	}
	_, err = db.Exec("INSERT INTO gmail_messages_fts(gmail_messages_fts) VALUES('rebuild')")
	return err
}

func rebuildAllGmailThreads(db *sql.DB) error {
	messages, err := loadAllGmailMessages(db)
	if err != nil { // nocov
		return err
	}
	threads := buildGmailThreadRecords(messages)
	if _, err := db.Exec(`DELETE FROM gmail_threads`); err != nil { // nocov
		return err
	}
	for _, thread := range threads {
		if _, err := db.Exec(`INSERT INTO gmail_threads
			(thread_id, subject, participants, message_count, first_date, last_date, last_message_id, thread_text_visible, last_synced)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			thread.threadID, thread.Subject, thread.Participants, thread.MessageCount,
			thread.FirstDate, thread.LastDate, thread.LastMessageID, thread.ThreadTextVisible); err != nil { // nocov
			return err
		}
	}
	return nil
}

func loadAllGmailMessages(db *sql.DB) ([]gmailMessageRecord, error) {
	rows, err := db.Query(`SELECT id, thread_id, subject, from_addr, to_addrs, cc_addrs, bcc_addrs, date, folder,
		labels, COALESCE(NULLIF(body_raw, ''), body_text), COALESCE(NULLIF(body_visible, ''), ''), has_attachments
		FROM gmail_messages`)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()
	return scanGmailMessages(rows)
}

func loadGmailMessagesByThread(db *sql.DB, threadID string) ([]gmailMessageRecord, error) {
	rows, err := db.Query(`SELECT id, thread_id, subject, from_addr, to_addrs, cc_addrs, bcc_addrs, date, folder,
		labels, COALESCE(NULLIF(body_raw, ''), body_text), COALESCE(NULLIF(body_visible, ''), ''), has_attachments
		FROM gmail_messages WHERE thread_id = ?`, threadID)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()
	return scanGmailMessages(rows)
}

func scanGmailMessages(rows *sql.Rows) ([]gmailMessageRecord, error) {
	var messages []gmailMessageRecord
	for rows.Next() {
		var msg gmailMessageRecord
		var bodyVisible string
		var hasAttachments int
		if err := rows.Scan(&msg.ID, &msg.ThreadID, &msg.Subject, &msg.From, &msg.To, &msg.CC, &msg.BCC,
			&msg.Date, &msg.Folder, &msg.Labels, &msg.BodyRaw, &bodyVisible, &hasAttachments); err != nil { // nocov
			return nil, err
		}
		msg.BodyVisible = bodyVisible
		if msg.BodyVisible == "" {
			msg.BodyVisible = deriveVisibleBody(msg.BodyRaw)
		}
		msg.HasAttachments = hasAttachments > 0
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil { // nocov
		return nil, err
	}
	sort.Slice(messages, func(i, j int) bool {
		return gmailMessageLess(messages[i], messages[j])
	})
	return messages, nil
}

func gmailMessageLess(a, b gmailMessageRecord) bool {
	ta := parseGmailMessageDate(a.Date)
	tb := parseGmailMessageDate(b.Date)
	switch {
	case !ta.IsZero() && !tb.IsZero() && !ta.Equal(tb):
		return ta.Before(tb)
	case !ta.IsZero() && tb.IsZero():
		return true
	case ta.IsZero() && !tb.IsZero():
		return false
	case a.Date != b.Date:
		return a.Date < b.Date
	default:
		return a.ID < b.ID
	}
}

func parseGmailMessageDate(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := mail.ParseDate(value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func buildGmailThreadRecords(messages []gmailMessageRecord) []gmailThreadRecord {
	grouped := make(map[string][]gmailMessageRecord)
	for _, msg := range messages {
		if msg.ThreadID == "" {
			continue
		}
		grouped[msg.ThreadID] = append(grouped[msg.ThreadID], msg)
	}
	threadIDs := make([]string, 0, len(grouped))
	for threadID := range grouped {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	records := make([]gmailThreadRecord, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		records = append(records, buildThreadRecord(grouped[threadID]))
	}
	return records
}

func buildThreadRecord(messages []gmailMessageRecord) gmailThreadRecord {
	if len(messages) == 0 {
		return gmailThreadRecord{}
	}
	sort.Slice(messages, func(i, j int) bool {
		return gmailMessageLess(messages[i], messages[j])
	})
	subject := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].Subject) != "" {
			subject = messages[i].Subject
			break
		}
	}
	first := messages[0]
	last := messages[len(messages)-1]
	return gmailThreadRecord{
		threadID:          first.ThreadID,
		Subject:           subject,
		Participants:      joinParticipants(messages),
		MessageCount:      len(messages),
		FirstDate:         first.Date,
		LastDate:          last.Date,
		LastMessageID:     last.ID,
		ThreadTextVisible: buildThreadTranscript(subject, joinParticipants(messages), messages),
	}
}

func joinParticipants(messages []gmailMessageRecord) string {
	seen := make(map[string]bool)
	var ordered []string
	appendAddresses := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			addr := strings.TrimSpace(part)
			if addr == "" || seen[addr] {
				continue
			}
			seen[addr] = true
			ordered = append(ordered, addr)
		}
	}
	for _, msg := range messages {
		appendAddresses(msg.From)
		appendAddresses(msg.To)
		appendAddresses(msg.CC)
		appendAddresses(msg.BCC)
	}
	return strings.Join(ordered, ", ")
}

func buildThreadTranscript(subject, participants string, messages []gmailMessageRecord) string {
	chunks := buildGmailThreadChunks(subject, participants, messages)
	var parts []string
	for _, chunk := range chunks {
		parts = append(parts, chunk.Content)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func loadGmailThreadMeta(db *sql.DB, threadID string) (gmailThreadRecord, error) {
	var record gmailThreadRecord
	err := db.QueryRow(`SELECT thread_id, subject, participants, message_count, first_date, last_date, last_message_id, thread_text_visible
		FROM gmail_threads WHERE thread_id = ?`, threadID).
		Scan(&record.threadID, &record.Subject, &record.Participants, &record.MessageCount,
			&record.FirstDate, &record.LastDate, &record.LastMessageID, &record.ThreadTextVisible)
	if err == sql.ErrNoRows {
		return gmailThreadRecord{}, nil
	}
	return record, err
}

func buildGmailThreadChunks(subject, participants string, messages []gmailMessageRecord) []gmailThreadChunk {
	const (
		targetSize = 3000
		maxSize    = 5000
	)
	header := formatThreadChunkHeader(subject, participants)
	var chunks []gmailThreadChunk
	var current strings.Builder
	currentLen := 0
	var chunkStartID, chunkEndID, chunkStartDate, chunkEndDate string
	hasEntries := false

	flush := func() {
		if !hasEntries {
			return
		}
		chunks = append(chunks, gmailThreadChunk{
			Content:        strings.TrimSpace(current.String()),
			StartMessageID: chunkStartID,
			EndMessageID:   chunkEndID,
			StartDate:      chunkStartDate,
			EndDate:        chunkEndDate,
		})
		current.Reset()
		currentLen = 0
		chunkStartID, chunkEndID, chunkStartDate, chunkEndDate = "", "", "", ""
		hasEntries = false
	}

	startChunk := func(msg gmailMessageRecord, content string) {
		current.Reset()
		current.WriteString(header)
		current.WriteString(content)
		currentLen = len(header) + len(content)
		chunkStartID, chunkEndID = msg.ID, msg.ID
		chunkStartDate, chunkEndDate = msg.Date, msg.Date
		hasEntries = true
	}

	for _, msg := range messages {
		entry := formatThreadTranscriptEntry(msg)
		if entry == "" {
			continue
		}
		if len(header)+len(entry) > maxSize {
			flush()
			for _, part := range splitLongThreadEntry(entry, maxSize-len(header)) {
				startChunk(msg, part)
				flush()
			}
			continue
		}
		if !hasEntries {
			startChunk(msg, entry)
			continue
		}
		if currentLen+2+len(entry) > targetSize {
			flush()
			startChunk(msg, entry)
			continue
		}
		current.WriteString("\n\n")
		current.WriteString(entry)
		currentLen += 2 + len(entry)
		chunkEndID = msg.ID
		chunkEndDate = msg.Date
	}
	flush()
	return chunks
}

func formatThreadChunkHeader(subject, participants string) string {
	var b strings.Builder
	if subject != "" {
		b.WriteString("Subject: ")
		b.WriteString(subject)
		b.WriteString("\n")
	}
	if participants != "" {
		b.WriteString("Participants: ")
		b.WriteString(participants)
		b.WriteString("\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

func formatThreadTranscriptEntry(msg gmailMessageRecord) string {
	body := strings.TrimSpace(msg.BodyVisible)
	if body == "" {
		body = strings.TrimSpace(msg.BodyRaw)
	}
	if body == "" {
		return ""
	}
	var b strings.Builder
	dateLabel := msg.Date
	if dateLabel == "" {
		dateLabel = "unknown date"
	}
	sender := msg.From
	if sender == "" {
		sender = "unknown sender"
	}
	b.WriteString("[")
	b.WriteString(dateLabel)
	b.WriteString("] ")
	b.WriteString(sender)
	b.WriteString("\n")
	b.WriteString(body)
	return b.String()
}

func splitLongThreadEntry(entry string, limit int) []string {
	if limit <= 0 || len(entry) <= limit {
		return []string{entry}
	}
	var parts []string
	remaining := entry
	for len(remaining) > limit {
		splitAt := strings.LastIndex(remaining[:limit], "\n")
		if splitAt <= 0 {
			splitAt = limit
		}
		parts = append(parts, strings.TrimSpace(remaining[:splitAt]))
		remaining = strings.TrimSpace(remaining[splitAt:])
	}
	if remaining != "" {
		parts = append(parts, remaining)
	}
	return parts
}
