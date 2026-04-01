package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"mcpyeahyouknowme/sources/gsuite"
)

func TestRunInfo_EndsWithBlankLine(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	oldVersion := BuildVersion
	oldTime := BuildTime
	BuildVersion = "test-version"
	BuildTime = "test-time"
	defer func() {
		BuildVersion = oldVersion
		BuildTime = oldTime
	}()

	runInfo()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "mcpyeahyouknowme info") {
		t.Fatalf("expected info header in output, got %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("expected output to end with a blank line, got suffix %q", got[max(0, len(got)-4):])
	}
}

func TestRenderInfo_containsSearchIndex(t *testing.T) {
	got := renderInfo()
	if !strings.Contains(got, "Search Index") {
		t.Fatalf("expected Search Index section in output, got %q", got)
	}
}

func TestWriteSearchIndexSection_partialIndexing(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSearchStore(dir, nil)
	if err != nil {
		t.Fatalf("NewSearchStore: %v", err)
	}
	defer store.Close()

	if err := store.IndexEntries(seedSearchEntries()); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}
	if _, err := store.db.Exec(
		"INSERT INTO search_embeddings (entry_id, embedding) VALUES (?, ?)",
		1, float32sToBytes([]float32{1, 2, 3}),
	); err != nil {
		t.Fatalf("insert embedding: %v", err)
	}

	var b strings.Builder
	writeSearchIndexSection(&b, dir, true)
	got := b.String()
	if !strings.Contains(got, "Indexed:") {
		t.Fatalf("expected Indexed label in output, got %q", got)
	}
	if strings.Contains(got, "Embedded:") {
		t.Fatalf("did not expect Embedded label in output, got %q", got)
	}
	if strings.Contains(got, "Last indexed:") {
		t.Fatalf("did not expect per-source indexing block in output, got %q", got)
	}
	if !strings.Contains(got, "indexing in progress") {
		t.Fatalf("expected indexing progress status in output, got %q", got)
	}
}

func TestWriteSearchIndexSection_partialIndexingDaemonStopped(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSearchStore(dir, nil)
	if err != nil {
		t.Fatalf("NewSearchStore: %v", err)
	}
	defer store.Close()

	if err := store.IndexEntries(seedSearchEntries()); err != nil {
		t.Fatalf("IndexEntries: %v", err)
	}

	var b strings.Builder
	writeSearchIndexSection(&b, dir, false)
	got := b.String()
	if !strings.Contains(got, "daemon not running") {
		t.Fatalf("expected daemon status in output, got %q", got)
	}
}

func TestRenderInfo_marksUnavailableSources(t *testing.T) {
	oldID := gsuite.GoogleClientID
	oldSecret := gsuite.GoogleClientSecret
	defer func() {
		gsuite.GoogleClientID = oldID
		gsuite.GoogleClientSecret = oldSecret
	}()

	gsuite.GoogleClientID = ""
	gsuite.GoogleClientSecret = ""

	got := renderInfo()
	if !strings.Contains(got, "Google Suite") {
		t.Fatalf("expected Google Suite section in output, got %q", got)
	}
	if !strings.Contains(got, "Status:     unavailable") {
		t.Fatalf("expected unavailable status in output, got %q", got)
	}
	if !strings.Contains(got, "GOOGLE_CLIENT_ID") {
		t.Fatalf("expected missing credential reason in output, got %q", got)
	}
}
