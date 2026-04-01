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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
