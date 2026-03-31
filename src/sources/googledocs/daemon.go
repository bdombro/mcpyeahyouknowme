package googledocs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// RequiresAuth returns true because Google Docs needs OAuth.
func (g *Source) RequiresAuth() bool { return true }

// StartCore runs the periodic Google Docs sync. Polls every 5 minutes.
func (g *Source) StartCore(ctx context.Context) error { // nocov
	fmt.Println("Starting Google Docs sync daemon...")

	if !g.isAuthenticated() { // nocov
		return fmt.Errorf("not authenticated - run 'mcpyeahyouknowme googledocs login' first")
	}

	return core.RunPollLoop(ctx, 5*time.Minute, g.syncDocuments)
}

// syncDocuments fetches and updates documents from the Google Docs API.
// On every cycle it lists ALL remote docs (metadata only), upserts new or
// modified ones, and deletes local rows whose IDs are no longer present
// remotely (trashed or deleted).
func (g *Source) syncDocuments(ctx context.Context) error { // nocov
	if g.db == nil { // nocov
		var err error
		g.db, err = openGoogleDocsDB(g.dataDir)
		if err != nil { // nocov
			return fmt.Errorf("cannot open database: %w", err)
		}
	}

	oauthConfig := g.getOAuthConfig()

	// If the stored token has no expiry (zero time), the oauth2 library
	// considers it valid and skips the refresh token exchange, sending the
	// stale access token until Google returns 401. Force a past expiry so
	// the library always refreshes before the first API call.
	syncToken := *g.token
	if syncToken.Expiry.IsZero() {
		syncToken.Expiry = time.Now().Add(-time.Second)
	}
	ts := oauthConfig.TokenSource(ctx, &syncToken)
	httpClient := oauth2.NewClient(ctx, ts)

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Drive service: %w", err)
	}

	docsService, err := docs.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Docs service: %w", err)
	}

	selfEmail := ""
	if data, err := os.ReadFile(filepath.Join(g.dataDir, "googledocs_email.txt")); err == nil {
		selfEmail = strings.TrimSpace(string(data))
	}

	remoteIDs := make(map[string]bool)
	var updatedCount int
	pageToken := ""
	for {
		fileList := driveService.Files.List().
			Q("mimeType='application/vnd.google-apps.document'").
			Fields("nextPageToken, files(id, name, modifiedTime, createdTime, webViewLink, owners(displayName, emailAddress))").
			IncludeItemsFromAllDrives(true).
			SupportsAllDrives(true).
			PageSize(100)

		if pageToken != "" { // nocov
			fileList = fileList.PageToken(pageToken)
		}

		res, err := fileList.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list documents: %w", err)
		}

		for _, file := range res.Files {
			remoteIDs[file.Id] = true

			if g.getLocalModifiedTime(file.Id) == file.ModifiedTime {
				continue
			}

			doc, err := docsService.Documents.Get(file.Id).Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to fetch document %s: %v\n", file.Id, err)
				continue
			}

			content := extractDocumentText(doc)
			owners := formatOwners(file.Owners, selfEmail)

			_, err = g.db.Exec(`
				INSERT OR REPLACE INTO documents 
				(id, title, content, modified_time, created_time, web_view_link, owners, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, file.Id, file.Name, content, file.ModifiedTime, file.CreatedTime,
				file.WebViewLink, owners, time.Now().Format(time.RFC3339))

			if err != nil { // nocov
				fmt.Printf("Warning: Failed to store document %s: %v\n", file.Id, err)
				continue
			}

			updatedCount++
		}

		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}

	deletedCount, err := g.deleteOrphanedDocuments(remoteIDs)
	if err != nil { // nocov
		fmt.Printf("Warning: Failed to delete orphaned documents: %v\n", err)
	}

	// Persist the refreshed access token so subsequent sync cycles don't
	// need to make an extra refresh round-trip.
	if fresh, err := ts.Token(); err == nil {
		if err2 := g.saveToken(fresh); err2 != nil { // nocov
			fmt.Printf("Warning: Failed to persist refreshed token: %v\n", err2)
		}
	}

	g.setLastSyncTime(time.Now())
	fmt.Printf("Google Docs sync complete: %d updated, %d deleted\n", updatedCount, deletedCount)
	return nil
}

// getLocalModifiedTime returns the stored modified_time for a document, or ""
// if the document is not in the local database.
func (g *Source) getLocalModifiedTime(docID string) string {
	var modTime string
	g.db.QueryRow("SELECT modified_time FROM documents WHERE id = ?", docID).Scan(&modTime)
	return modTime
}

// deleteOrphanedDocuments removes local documents that are no longer present
// in the remote listing and returns the number of rows deleted.
func (g *Source) deleteOrphanedDocuments(remoteIDs map[string]bool) (int, error) {
	rows, err := g.db.Query("SELECT id FROM documents")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var toDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil { // nocov
			continue
		}
		if !remoteIDs[id] {
			toDelete = append(toDelete, id)
		}
	}
	rows.Close()

	for _, id := range toDelete {
		g.db.Exec("DELETE FROM documents WHERE id = ?", id)
	}
	return len(toDelete), nil
}

// formatOwners returns a comma-separated "Name <email>" string for each owner,
// excluding the currently logged-in user (by email).
func formatOwners(owners []*drive.User, selfEmail string) string {
	parts := make([]string, 0, len(owners))
	for _, o := range owners {
		if selfEmail != "" && strings.EqualFold(o.EmailAddress, selfEmail) {
			continue
		}
		if o.DisplayName != "" && o.EmailAddress != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", o.DisplayName, o.EmailAddress))
		} else if o.EmailAddress != "" {
			parts = append(parts, o.EmailAddress)
		} else if o.DisplayName != "" {
			parts = append(parts, o.DisplayName)
		}
	}
	return strings.Join(parts, ", ")
}

// extractDocumentText extracts plain text from a Google Doc.
func extractDocumentText(doc *docs.Document) string {
	var text string
	for _, element := range doc.Body.Content {
		if element.Paragraph != nil {
			for _, elem := range element.Paragraph.Elements {
				if elem.TextRun != nil {
					text += elem.TextRun.Content
				}
			}
		}
	}
	return text
}

func (g *Source) getLastSyncTime() time.Time {
	if g.db == nil {
		return time.Time{}
	}
	var value string
	err := g.db.QueryRow("SELECT value FROM sync_state WHERE key = 'last_sync'").Scan(&value)
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, value)
	return t
}

func (g *Source) setLastSyncTime(t time.Time) {
	if g.db == nil {
		return
	}
	g.db.Exec(`
		INSERT OR REPLACE INTO sync_state (key, value)
		VALUES ('last_sync', ?)
	`, t.Format(time.RFC3339))
}
