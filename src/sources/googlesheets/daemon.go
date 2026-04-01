package googlesheets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// RequiresAuth returns true because Google Sheets needs OAuth.
func (g *Source) RequiresAuth() bool { return true }

// StartCore runs the periodic Google Sheets sync. Polls every 5 minutes.
func (g *Source) StartCore(ctx context.Context) error { // nocov
	fmt.Println("Starting Google Sheets sync daemon...")

	if !g.isAuthenticated() { // nocov
		return fmt.Errorf("not authenticated - run 'mcpyeahyouknowme googlesheets login' first")
	}

	return core.RunPollLoop(ctx, 5*time.Minute, g.syncSpreadsheets)
}

// syncSpreadsheets fetches and updates spreadsheets from the Google Sheets API.
// On every cycle it lists ALL remote spreadsheets (metadata only), upserts new or
// modified ones, and deletes local rows whose IDs are no longer present
// remotely (trashed or deleted).
func (g *Source) syncSpreadsheets(ctx context.Context) error { // nocov
	if g.db == nil { // nocov
		var err error
		g.db, err = openGoogleSheetsDB(g.dataDir)
		if err != nil { // nocov
			return fmt.Errorf("cannot open database: %w", err)
		}
	}

	oauthConfig := g.getOAuthConfig()

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

	sheetsService, err := sheets.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil { // nocov
		return fmt.Errorf("failed to create Sheets service: %w", err)
	}

	selfEmail := ""
	if data, err := os.ReadFile(filepath.Join(g.dataDir, "googlesheets_email.txt")); err == nil {
		selfEmail = strings.TrimSpace(string(data))
	}

	remoteIDs := make(map[string]bool)
	var updatedCount int
	pageToken := ""
	for {
		fileList := driveService.Files.List().
			Q("mimeType='application/vnd.google-apps.spreadsheet'").
			Fields("nextPageToken, files(id, name, modifiedTime, createdTime, webViewLink, owners(displayName, emailAddress))").
			IncludeItemsFromAllDrives(true).
			SupportsAllDrives(true).
			PageSize(100)

		if pageToken != "" { // nocov
			fileList = fileList.PageToken(pageToken)
		}

		res, err := fileList.Do()
		if err != nil { // nocov
			return fmt.Errorf("failed to list spreadsheets: %w", err)
		}

		for _, file := range res.Files {
			remoteIDs[file.Id] = true

			if g.getLocalModifiedTime(file.Id) == file.ModifiedTime {
				continue
			}

			spreadsheet, err := sheetsService.Spreadsheets.Get(file.Id).Do()
			if err != nil { // nocov
				fmt.Printf("Warning: Failed to fetch spreadsheet %s: %v\n", file.Id, err)
				continue
			}

			content := extractSpreadsheetText(spreadsheet)
			owners := formatDriveOwners(file.Owners, selfEmail)
			sheetCount := len(spreadsheet.Sheets)

			_, err = g.db.Exec(`
				INSERT OR REPLACE INTO spreadsheets
				(id, title, content, modified_time, created_time, web_view_link, owners, sheet_count, last_synced)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, file.Id, file.Name, content, file.ModifiedTime, file.CreatedTime,
				file.WebViewLink, owners, sheetCount, time.Now().Format(time.RFC3339))

			if err != nil { // nocov
				fmt.Printf("Warning: Failed to store spreadsheet %s: %v\n", file.Id, err)
				continue
			}

			updatedCount++
		}

		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}

	deletedCount, err := g.deleteOrphanedSpreadsheets(remoteIDs)
	if err != nil { // nocov
		fmt.Printf("Warning: Failed to delete orphaned spreadsheets: %v\n", err)
	}

	if fresh, err := ts.Token(); err == nil {
		if err2 := g.saveToken(fresh); err2 != nil { // nocov
			fmt.Printf("Warning: Failed to persist refreshed token: %v\n", err2)
		}
	}

	g.setLastSyncTime(time.Now())
	fmt.Printf("Google Sheets sync complete: %d updated, %d deleted\n", updatedCount, deletedCount)
	return nil
}

// getLocalModifiedTime returns the stored modified_time for a spreadsheet, or ""
// if the spreadsheet is not in the local database.
func (g *Source) getLocalModifiedTime(sheetID string) string {
	var modTime string
	g.db.QueryRow("SELECT modified_time FROM spreadsheets WHERE id = ?", sheetID).Scan(&modTime)
	return modTime
}

// deleteOrphanedSpreadsheets removes local spreadsheets that are no longer present
// in the remote listing and returns the number of rows deleted.
func (g *Source) deleteOrphanedSpreadsheets(remoteIDs map[string]bool) (int, error) {
	rows, err := g.db.Query("SELECT id FROM spreadsheets")
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
		g.db.Exec("DELETE FROM spreadsheets WHERE id = ?", id)
	}
	return len(toDelete), nil
}

// formatDriveOwners adapts drive.User owners to the local formatOwners helper.
func formatDriveOwners(owners []*drive.User, selfEmail string) string {
	adapted := make([]sheetOwner, len(owners))
	for i, o := range owners {
		adapted[i] = sheetOwner{DisplayName: o.DisplayName, EmailAddress: o.EmailAddress}
	}
	return formatOwners(adapted, selfEmail)
}

// extractSpreadsheetText extracts plain text from a Google Spreadsheet.
// Each sheet is rendered as "## SheetName\n" followed by tab-separated rows.
func extractSpreadsheetText(spreadsheet *sheets.Spreadsheet) string {
	var b strings.Builder
	for i, sheet := range spreadsheet.Sheets {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## ")
		b.WriteString(sheet.Properties.Title)
		b.WriteString("\n")

		if sheet.Data == nil {
			continue
		}
		for _, gridData := range sheet.Data {
			for _, row := range gridData.RowData {
				var cells []string
				for _, cell := range row.Values {
					cells = append(cells, cell.FormattedValue)
				}
				b.WriteString(strings.Join(cells, "\t"))
				b.WriteString("\n")
			}
		}
	}
	return b.String()
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
