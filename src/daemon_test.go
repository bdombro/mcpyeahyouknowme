package main

import (
	"path/filepath"
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
		"mcp":         true,
		"info":        true,
		"completions": true,
		"core":        true,
		"start":       true,
		"stop":        true,
		"restart":     true,
		"reindex":     true,
		"uninstall":   true,
		"whatsapp":    true,
		"gsuite":      true,
		"login":       true,
		"reset":       true,
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
