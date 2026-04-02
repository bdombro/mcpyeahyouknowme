package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verifies plistPath returns an absolute LaunchAgents plist path for the installed daemon.
func TestPlistPath(t *testing.T) {
	path := plistPath()

	if !filepath.IsAbs(path) {
		t.Error("plistPath should return an absolute path")
	}

	expectedFilename := plistName + ".plist"
	if filepath.Base(path) != expectedFilename {
		t.Errorf("plistPath should end with %q, got %q", expectedFilename, filepath.Base(path))
	}

	if !pathContains(path, "LaunchAgents") {
		t.Errorf("plistPath should contain LaunchAgents directory, got %q", path)
	}
}

// Verifies the top-level command list still contains the expected public CLI surface.
func TestCommandsListCompleteness(t *testing.T) {
	expected := map[string]bool{
		"mcp":             true,
		"info":            true,
		"completions":     true,
		"core":            true,
		"start":           true,
		"stop":            true,
		"restart":         true,
		"reindex":         true,
		"uninstall":       true,
		"whatsapp":        true,
		"gsuite":          true,
		"browser_history": true,
		"notebook":        true,
		"login":           true,
		"reset":           true,
	}

	for _, cmd := range commandNames(topLevelCommands()) {
		if !expected[cmd] {
			t.Errorf("Unexpected command in list: %q", cmd)
		}
		delete(expected, cmd)
	}

	if len(expected) > 0 {
		t.Errorf("Missing expected commands: %v", expected)
	}
}

// Verifies the LaunchAgent label constant stays aligned with the installed plist name.
func TestPlistName(t *testing.T) {
	expectedName := "com.mcpyeahyouknowme.core"
	if plistName != expectedName {
		t.Errorf("plistName = %q, want %q", plistName, expectedName)
	}
}

// Verifies generated bash completions stay aligned with dynamic command metadata and constrained arguments.
func TestPrintBashCompletions_ContainsBrowserHistoryOptions(t *testing.T) {
	out := captureStdout(t, printBashCompletions)
	for _, want := range []string{
		`browser_history)`,
		`compgen -W "enable reset"`,
		`browser_history:enable)`,
		`compgen -W "chrome brave"`,
		`completions)`,
		`compgen -W "bash zsh"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("printBashCompletions() missing %q in output:\n%s", want, out)
		}
	}
}

// Verifies generated zsh completions stay aligned with dynamic command metadata and constrained arguments.
func TestPrintZshCompletions_ContainsBrowserHistoryOptions(t *testing.T) {
	out := captureStdout(t, printZshCompletions)
	for _, want := range []string{
		`browser_history)`,
		`'enable:Enable history indexing for chrome or brave'`,
		`browser_history:enable)`,
		`'chrome:Google Chrome history'`,
		`'brave:Brave Browser history'`,
		`'bash:Bash completions'`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("printZshCompletions() missing %q in output:\n%s", want, out)
		}
	}
}

// Returns stdout from fn so completion-rendering tests can assert on generated shell scripts.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}

// Returns whether path contains component so plist-path assertions can stay platform-safe.
func pathContains(path, component string) bool {
	dir := path
	for dir != "." && dir != "/" {
		if filepath.Base(dir) == component {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}
