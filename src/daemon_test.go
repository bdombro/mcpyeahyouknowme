package main

import (
	"path/filepath"
	"testing"
)

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

func TestCommandsListCompleteness(t *testing.T) {
	expected := map[string]bool{
		"mcp":         true,
		"info":        true,
		"completions": true,
		"core":        true,
		"start":       true,
		"stop":        true,
		"restart":     true,
		"uninstall":   true,
		"whatsapp":    true,
		"gsuite":      true,
		"login":       true,
		"reset":       true,
	}

	for _, cmd := range commands {
		if !expected[cmd] {
			t.Errorf("Unexpected command in list: %q", cmd)
		}
		delete(expected, cmd)
	}

	if len(expected) > 0 {
		t.Errorf("Missing expected commands: %v", expected)
	}
}

func TestPlistName(t *testing.T) {
	expectedName := "com.mcpyeahyouknowme.core"
	if plistName != expectedName {
		t.Errorf("plistName = %q, want %q", plistName, expectedName)
	}
}

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
