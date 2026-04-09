package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"mcpyeahyouknowme/core"
)

// mockProfileStore implements profileToolStore for testing.
type mockProfileStore struct {
	results map[string][]SearchResult
	err     error
	errors  map[string]error
}

// Search returns seeded results for the given query, or an error if configured.
// Per-query errors in the errors map take priority over the global err field.
func (m *mockProfileStore) Search(query string, _ int, _, typeFilter, _, _ string) ([]SearchResult, error) {
	if m.errors != nil {
		if e, ok := m.errors[query]; ok {
			return nil, e
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	key := query
	if typeFilter != "" {
		key = query + ":" + typeFilter
	}
	if r, ok := m.results[key]; ok {
		return r, nil
	}
	return nil, nil
}

// chunkMeta builds JSON metadata for a note chunk entry.
func chunkMeta(t *testing.T, idx int) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(map[string]interface{}{"chunk": idx})
	return b
}

// Verifies the profile tool returns the "About Me" note with a referenced section.
func TestProfileTool_success(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			// Title search for "About Me"
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			// Chunk search for the note body
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "First chunk.", Metadata: chunkMeta(t, 0)},
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "Third chunk.", Metadata: chunkMeta(t, 2)},
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "Second chunk. See also [[Work History]].", Metadata: chunkMeta(t, 1)},
			},
			// Title search for referenced note
			"Work History": {
				{Source: "notebook", ContentType: "note_title", Title: "Work History", Content: "Work History"},
			},
			// Chunk search for the referenced note
			"Work History:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "Work History", Content: "10 years experience.", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	result, err := buildProfile(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "About Me" {
		t.Errorf("expected title 'About Me', got %q", result.Title)
	}
	if result.Source != "notebook" {
		t.Errorf("expected source 'notebook', got %q", result.Source)
	}

	// Content should be chunks in order (0, 1, 2).
	if !strings.Contains(result.Content, "First chunk.") {
		t.Error("expected content to contain 'First chunk.'")
	}
	if !strings.Contains(result.Content, "Second chunk.") {
		t.Error("expected content to contain 'Second chunk.'")
	}
	if !strings.Contains(result.Content, "Third chunk.") {
		t.Error("expected content to contain 'Third chunk.'")
	}
	idx0 := strings.Index(result.Content, "First chunk.")
	idx1 := strings.Index(result.Content, "Second chunk.")
	idx2 := strings.Index(result.Content, "Third chunk.")
	if !(idx0 < idx1 && idx1 < idx2) {
		t.Errorf("chunks out of order: positions %d, %d, %d", idx0, idx1, idx2)
	}

	if len(result.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(result.Sections))
	}
	if result.Sections[0].Title != "Work History" {
		t.Errorf("expected section title 'Work History', got %q", result.Sections[0].Title)
	}
	if !strings.Contains(result.Sections[0].Content, "10 years experience.") {
		t.Error("expected section content to contain '10 years experience.'")
	}
}

// Verifies the profile tool returns an error when no "About Me" note exists.
func TestProfileTool_noNoteFound(t *testing.T) {
	store := &mockProfileStore{results: map[string][]SearchResult{}}
	_, err := buildProfile(store)
	if err == nil {
		t.Fatal("expected error when no note found")
	}
	if !strings.Contains(err.Error(), "About Me") {
		t.Errorf("expected error to mention 'About Me', got %q", err.Error())
	}
}

// Verifies the profile tool propagates store errors from the title search.
func TestProfileTool_storeError(t *testing.T) {
	store := &mockProfileStore{err: errors.New("db unavailable")}
	_, err := buildProfile(store)
	if err == nil {
		t.Fatal("expected error on store failure")
	}
	if !strings.Contains(err.Error(), "db unavailable") {
		t.Errorf("expected store error to propagate, got %q", err.Error())
	}
}

// Verifies the profile tool adds missing referenced notes to SkippedRefs instead of silently dropping them.
func TestProfileTool_missingReferencedNote(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "See [[Missing Note]].", Metadata: chunkMeta(t, 0)},
			},
			// "Missing Note" is not seeded.
		},
	}

	result, err := buildProfile(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Sections) != 0 {
		t.Errorf("expected no sections when referenced note is missing, got %d", len(result.Sections))
	}
	if len(result.SkippedRefs) != 1 || result.SkippedRefs[0] != "Missing Note" {
		t.Errorf("expected SkippedRefs=[\"Missing Note\"], got %v", result.SkippedRefs)
	}
}

// Verifies the profile tool adds errored referenced notes to SkippedRefs when fetchNote returns an error.
func TestProfileTool_erroredReferencedNote(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "See [[Error Note]].", Metadata: chunkMeta(t, 0)},
			},
		},
		errors: map[string]error{
			"Error Note": fmt.Errorf("search index unavailable"),
		},
	}

	result, err := buildProfile(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Sections) != 0 {
		t.Errorf("expected no sections when referenced note errors, got %d", len(result.Sections))
	}
	if len(result.SkippedRefs) != 1 || result.SkippedRefs[0] != "Error Note" {
		t.Errorf("expected SkippedRefs=[\"Error Note\"], got %v", result.SkippedRefs)
	}
}

// Verifies the profile tool skips duplicate section references.
func TestProfileTool_deduplicateSections(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "See [[Skills]] and [[skills]].", Metadata: chunkMeta(t, 0)},
			},
			"Skills": {
				{Source: "notebook", ContentType: "note_title", Title: "Skills", Content: "Skills"},
			},
			"Skills:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "Skills", Content: "Go, TypeScript.", Metadata: chunkMeta(t, 0)},
			},
			"skills": {
				{Source: "notebook", ContentType: "note_title", Title: "Skills", Content: "Skills"},
			},
			"skills:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "Skills", Content: "Go, TypeScript.", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	result, err := buildProfile(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Sections) != 1 {
		t.Errorf("expected 1 deduplicated section, got %d", len(result.Sections))
	}
}

// Verifies the profile tool extracts markdown link references (non-URL, non-anchor).
func TestProfileTool_markdownLinkRefs(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "See [my work](Work History.md).", Metadata: chunkMeta(t, 0)},
			},
			"Work History": {
				{Source: "notebook", ContentType: "note_title", Title: "Work History", Content: "Work History"},
			},
			"Work History:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "Work History", Content: "Engineer.", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	result, err := buildProfile(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Sections) != 1 {
		t.Fatalf("expected 1 section from markdown link, got %d", len(result.Sections))
	}
	if result.Sections[0].Title != "Work History" {
		t.Errorf("expected section title 'Work History', got %q", result.Sections[0].Title)
	}
}

// Verifies the profile tool does not follow external URL markdown links.
func TestProfileTool_skipsURLLinks(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "Visit [my site](https://example.com).", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	result, err := buildProfile(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Sections) != 0 {
		t.Errorf("expected no sections for URL link, got %d", len(result.Sections))
	}
}

// Verifies the profile tool is registered as profile_about_me and returns the expected JSON shape via MCP JSON-RPC.
func TestProfileTool_mcpRegistration(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "Hello world.", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	s := newTestMCPServerWithProfile(t, store)
	text := callGlobalTool(t, s, "profile_about_me", map[string]interface{}{})

	var result ProfileResult
	if err := core.UnmarshalToolResultTextPayload(text, &result); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, text)
	}
	if result.Title != "About Me" {
		t.Errorf("expected title 'About Me', got %q", result.Title)
	}
	if !strings.Contains(result.Content, "Hello world.") {
		t.Errorf("expected content to contain 'Hello world.', got %q", result.Content)
	}
}

// Verifies the profile tool returns an MCP error result when no "About Me" note is found.
func TestProfileTool_mcpRegistration_noNote(t *testing.T) {
	store := &mockProfileStore{results: map[string][]SearchResult{}}
	s := newTestMCPServerWithProfile(t, store)
	text := callGlobalTool(t, s, "profile_about_me", map[string]interface{}{})
	if !strings.Contains(text, "About Me") {
		t.Errorf("expected MCP error to mention 'About Me', got %q", text)
	}
}

// Verifies the extractNoteRefs function handles wikilinks, aliases, and deduplication.
func TestExtractNoteRefs_wikilinks(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"simple wikilink", "See [[Work History]].", []string{"Work History"}},
		{"aliased wikilink", "See [[Work History|my jobs]].", []string{"Work History"}},
		{"deduplication", "[[Skills]] and [[skills]].", []string{"Skills"}},
		{"multiple", "[[A]] and [[B]].", []string{"A", "B"}},
		{"markdown relative link", "[jobs](work.md)", []string{"work"}},
		{"markdown url ignored", "[site](https://x.com)", []string{}},
		{"empty content", "", []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractNoteRefs(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("index %d: expected %q, got %q", i, w, got[i])
				}
			}
		})
	}
}

// Verifies the profile tool caps output at 5 references regardless of content.
func TestExtractNoteRefs_cap(t *testing.T) {
	content := "[[A]] [[B]] [[C]] [[D]] [[E]] [[F]] [[G]]"
	refs := extractNoteRefs(content)
	if len(refs) != 5 {
		t.Errorf("expected 5 refs (cap), got %d: %v", len(refs), refs)
	}
}

// Verifies stemLinkPath strips directory and extension components from a path.
func TestStemLinkPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"work.md", "work"},
		{"notes/Work History.md", "Work History"},
		{"file", "file"},
		{"a/b/c.txt", "c"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := stemLinkPath(tc.in)
			if got != tc.want {
				t.Errorf("stemLinkPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Verifies the profile tool deduplicates a reference that matches the root note title.
func TestProfileTool_selfReferenceDedup(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "See also [[About Me]].", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	result, err := buildProfile(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Sections) != 0 {
		t.Errorf("expected no sections when note references itself, got %d", len(result.Sections))
	}
}

// Verifies fetchNote prefers an exact title match over the first non-exact result.
func TestFetchNote_exactTitleMatchBreaks(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me and More", Content: "About Me and More"},
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "Exact match content.", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	title, _, _, err := fetchNote(store, "About Me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "About Me" {
		t.Errorf("expected exact title 'About Me', got %q", title)
	}
}

// Verifies fetchNote propagates errors from the chunk search step.
func TestFetchNote_chunkSearchError(t *testing.T) {
	callCount := 0
	store := &mockProfileStoreFunc{
		searchFn: func(_ string, _ int, _, _, _, _ string) ([]SearchResult, error) {
			callCount++
			if callCount == 1 {
				return []SearchResult{
					{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
				}, nil
			}
			return nil, errors.New("chunk search failed")
		},
	}

	_, _, _, err := fetchNote(store, "About Me")
	if err == nil {
		t.Fatal("expected error from chunk search failure")
	}
	if !strings.Contains(err.Error(), "chunk search failed") {
		t.Errorf("expected chunk search error to propagate, got %q", err.Error())
	}
}

// Verifies fetchNote filters out chunks whose title does not match the found note.
func TestFetchNote_mismatchedChunkTitlesFiltered(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "Good chunk.", Metadata: chunkMeta(t, 0)},
				{Source: "notebook", ContentType: "note_content", Title: "Other Note", Content: "Noise chunk.", Metadata: chunkMeta(t, 1)},
			},
		},
	}

	_, content, _, err := fetchNote(store, "About Me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(content, "Good chunk.") {
		t.Errorf("expected content to include matching chunk, got %q", content)
	}
	if strings.Contains(content, "Noise chunk.") {
		t.Errorf("expected mismatched chunk to be filtered out, got %q", content)
	}
}

// mockProfileStoreFunc is a profileToolStore backed by a function for fine-grained call control.
type mockProfileStoreFunc struct {
	searchFn func(query string, limit int, sourceFilter, typeFilter, after, before string) ([]SearchResult, error)
}

// Search delegates to the injected function.
func (m *mockProfileStoreFunc) Search(query string, limit int, sf, tf, after, before string) ([]SearchResult, error) {
	return m.searchFn(query, limit, sf, tf, after, before)
}

// Verifies fetchNote skips results whose content type is not a recognized title type.
func TestFetchNote_skipsUnknownContentType(t *testing.T) {
	store := &mockProfileStore{
		results: map[string][]SearchResult{
			"About Me": {
				// chat_content is not in titleToContentType; should be skipped.
				{Source: "whatsapp", ContentType: "chat_content", Title: "About Me", Content: "noise"},
				{Source: "notebook", ContentType: "note_title", Title: "About Me", Content: "About Me"},
			},
			"About Me:note_content": {
				{Source: "notebook", ContentType: "note_content", Title: "About Me", Content: "Profile text.", Metadata: chunkMeta(t, 0)},
			},
		},
	}

	title, content, _, err := fetchNote(store, "About Me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "About Me" {
		t.Errorf("expected title 'About Me', got %q", title)
	}
	if !strings.Contains(content, "Profile text.") {
		t.Errorf("expected notebook content, got %q", content)
	}
}

// Verifies extractNoteRefs caps at 5 when the limit is reached inside the markdown link loop.
func TestExtractNoteRefs_capInMarkdownLoop(t *testing.T) {
	// 4 wikilinks + 2 markdown links → cap fires in the markdown loop at the 5th ref.
	content := "[[A]] [[B]] [[C]] [[D]] [e](e.md) [f](f.md)"
	refs := extractNoteRefs(content)
	if len(refs) != 5 {
		t.Errorf("expected 5 refs (cap in md loop), got %d: %v", len(refs), refs)
	}
}


// Builds a minimal MCP server with only the profile tool registered for tool-level integration tests.
func newTestMCPServerWithProfile(t *testing.T, store profileToolStore) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	RegisterProfileTool(s, store)
	return s
}
