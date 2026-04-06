package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mcpyeahyouknowme/core"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const profileToolDescription = `Returns structured profile information about the MCP owner. ` +
	`Searches all connected data sources for a note titled "AGENTS About Me", reconstructs its full content from ` +
	`indexed chunks, extracts any note references (wikilinks or markdown links), and aggregates those ` +
	`referenced notes as separate sections. Call this before making personalized recommendations or ` +
	`when the user's background is relevant.`

const profileToolExample = `{}`

// titleToContentType maps a title content type to the companion chunk content type used to reconstruct a full entry.
var titleToContentType = map[string]string{
	"note_title":           "note_content",
	"pdf_title":            "pdf_content",
	"document_title":       "document_content",
	"spreadsheet_title":    "spreadsheet_content",
	"presentation_title":   "presentation_content",
}

// wikiLinkRe matches Obsidian-style wikilinks: [[Note Name]] or [[Note Name|Alias]].
var wikiLinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)

// mdLinkRe matches relative markdown links: [text](path) where path is not a URL or anchor.
var mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)#]+)\)`)

// profileToolStore abstracts the search store for profile tool usage.
type profileToolStore interface {
	Search(query string, limit int, sourceFilter, typeFilter string) ([]SearchResult, error)
}

// ProfileResult is returned by the profile_about_me tool.
type ProfileResult struct {
	Title    string           `json:"title"`
	Content  string           `json:"content"`
	Source   string           `json:"source"`
	Sections []ProfileSection `json:"sections"`
}

// ProfileSection represents a note referenced by the "AGENTS About Me" note.
type ProfileSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Source  string `json:"source"`
}

// RegisterProfileTool registers the profile_about_me tool, which aggregates the owner's
// "AGENTS About Me" note and any notes it references into a single structured response.
func RegisterProfileTool(s *server.MCPServer, store profileToolStore) {
	s.AddTool(core.NewReadOnlyTool("profile_about_me",
		core.ToolDescription(profileToolDescription, profileToolExample),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := buildProfile(store)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return core.JsonResult(result)
	})
}

// buildProfile searches for an "AGENTS About Me" note, reconstructs its full content, extracts
// note references, and aggregates each referenced note as a section.
func buildProfile(store profileToolStore) (*ProfileResult, error) {
	title, content, source, err := fetchNote(store, "AGENTS About Me")
	if err != nil {
		return nil, err
	}
	if title == "" {
		return nil, fmt.Errorf("no 'AGENTS About Me' note found in connected data sources")
	}

	refs := extractNoteRefs(content)
	result := &ProfileResult{
		Title:   title,
		Content: content,
		Source:  source,
	}

	seen := map[string]bool{strings.ToLower(title): true}
	for _, ref := range refs {
		key := strings.ToLower(ref)
		if seen[key] {
			continue
		}
		seen[key] = true

		refTitle, refContent, refSource, refErr := fetchNote(store, ref)
		if refErr != nil || refTitle == "" {
			continue
		}
		result.Sections = append(result.Sections, ProfileSection{
			Title:   refTitle,
			Content: refContent,
			Source:  refSource,
		})
	}

	return result, nil
}

// fetchNote finds the best-matching note for query in the search index, then reconstructs
// its full content by collecting and sorting all indexed content chunks for that note.
// Returns empty strings (no error) when no matching note is found.
func fetchNote(store profileToolStore, query string) (title, content, source string, err error) {
	titleResults, err := store.Search(query, 10, "", "")
	if err != nil {
		return "", "", "", err
	}

	// Pick the best result that has a known title content type, preferring exact title matches.
	var best *SearchResult
	for i := range titleResults {
		r := &titleResults[i]
		if _, ok := titleToContentType[r.ContentType]; !ok {
			continue
		}
		if best == nil {
			best = r
		}
		if strings.EqualFold(r.Title, query) {
			best = r
			break
		}
	}
	if best == nil {
		return "", "", "", nil
	}

	noteTitle := best.Title
	noteSource := best.Source
	contentType := titleToContentType[best.ContentType]

	// Fetch up to 50 content chunks for this note and reconstruct in order.
	chunkResults, err := store.Search(noteTitle, 50, noteSource, contentType)
	if err != nil {
		return "", "", "", err
	}

	type chunkEntry struct {
		idx     int
		content string
	}
	var chunks []chunkEntry
	for _, r := range chunkResults {
		if !strings.EqualFold(r.Title, noteTitle) {
			continue
		}
		var meta struct {
			Chunk int `json:"chunk"`
		}
		if len(r.Metadata) > 0 {
			json.Unmarshal(r.Metadata, &meta) //nolint:errcheck
		}
		chunks = append(chunks, chunkEntry{idx: meta.Chunk, content: r.Content})
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].idx < chunks[j].idx })

	var parts []string
	for _, c := range chunks {
		if strings.TrimSpace(c.content) != "" {
			parts = append(parts, c.content)
		}
	}

	return noteTitle, strings.Join(parts, "\n\n"), noteSource, nil
}

// extractNoteRefs parses wikilinks and relative markdown links from content,
// returning a deduplicated list of referenced note names (up to 5).
func extractNoteRefs(content string) []string {
	seen := map[string]bool{}
	var refs []string

	for _, m := range wikiLinkRe.FindAllStringSubmatch(content, -1) {
		name := strings.TrimSpace(m[1])
		lower := strings.ToLower(name)
		if name != "" && !seen[lower] {
			seen[lower] = true
			refs = append(refs, name)
		}
		if len(refs) >= 5 {
			return refs
		}
	}

	for _, m := range mdLinkRe.FindAllStringSubmatch(content, -1) {
		link := strings.TrimSpace(m[2])
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			continue
		}
		name := stemLinkPath(link)
		lower := strings.ToLower(name)
		if name != "" && !seen[lower] {
			seen[lower] = true
			refs = append(refs, name)
		}
		if len(refs) >= 5 {
			return refs
		}
	}

	return refs
}

// stemLinkPath extracts a display name from a relative markdown link target by stripping
// the directory and file extension so callers can use it as a note title for search.
func stemLinkPath(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}
