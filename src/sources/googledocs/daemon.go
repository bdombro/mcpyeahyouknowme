package googledocs

import (
	"context"
	"fmt"
	"time"

	"mcpyeahyouknowme/core"

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
func (g *Source) syncDocuments(ctx context.Context) error { // nocov
	if g.db == nil { // nocov
		var err error
		g.db, err = openGoogleDocsDB(g.dataDir)
		if err != nil { // nocov
			return fmt.Errorf("cannot open database: %w", err)
		}
	}

	oauthConfig := g.getOAuthConfig()
	httpClient := oauthConfig.Client(ctx, g.token)

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Drive service: %w", err)
	}

	docsService, err := docs.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Docs service: %w", err)
	}

	lastSync := g.getLastSyncTime()

	query := "mimeType='application/vnd.google-apps.document'"
	if !lastSync.IsZero() { // nocov
		query += fmt.Sprintf(" and modifiedTime > '%s'", lastSync.Format(time.RFC3339))
	}

	var updatedCount int
	pageToken := ""
	for {
		fileList := driveService.Files.List().
			Q(query).
			Fields("nextPageToken, files(id, name, modifiedTime, createdTime, webViewLink)").
			PageSize(100)

		if pageToken != "" { // nocov
			fileList = fileList.PageToken(pageToken)
		}

		res, err := fileList.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list documents: %w", err)
		}

		for _, file := range res.Files {
			doc, err := docsService.Documents.Get(file.Id).Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to fetch document %s: %v\n", file.Id, err)
				continue
			}

			content := extractDocumentText(doc)

			_, err = g.db.Exec(`
				INSERT OR REPLACE INTO documents 
				(id, title, content, modified_time, created_time, web_view_link, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, file.Id, file.Name, content, file.ModifiedTime, file.CreatedTime,
				file.WebViewLink, time.Now().Format(time.RFC3339))

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

	g.setLastSyncTime(time.Now())
	fmt.Printf("Google Docs sync complete: %d documents updated\n", updatedCount)
	return nil
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
