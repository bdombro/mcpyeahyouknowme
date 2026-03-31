package main

import (
	"path/filepath"
	"testing"
)

func TestPlistPath(t *testing.T) {
	path := plistPath()
	
	// Should end with the correct filename
	if !filepath.IsAbs(path) {
		t.Error("plistPath should return an absolute path")
	}
	
	expectedFilename := plistName + ".plist"
	if filepath.Base(path) != expectedFilename {
		t.Errorf("plistPath should end with %q, got %q", expectedFilename, filepath.Base(path))
	}
	
	// Should contain LaunchAgents in path
	if !contains(path, "LaunchAgents") {
		t.Errorf("plistPath should contain LaunchAgents directory, got %q", path)
	}
}

func TestIsLoggedIn(t *testing.T) {
	// Test that the function executes without panic
	// The actual result depends on the real system state
	loggedIn := isLoggedIn()
	
	// We just verify it returns a boolean
	_ = loggedIn
}

func TestCommandsListCompleteness(t *testing.T) {
	// Verify all expected commands are in the list
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
		"googledocs":  true,
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
	// Verify plistName constant is set correctly
	expectedName := "com.mcpyeahyouknowme.core"
	if plistName != expectedName {
		t.Errorf("plistName = %q, want %q", plistName, expectedName)
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && 
		   (s == substr || 
		    filepath.Base(s) == substr ||
			s[len(s)-len(substr):] == substr ||
			pathContains(s, substr))
}

func pathContains(path, component string) bool {
	parts := filepath.SplitList(path)
	for _, part := range parts {
		if part == component {
			return true
		}
	}
	// Also check path separators
	dir := path
	for dir != "." && dir != "/" {
		if filepath.Base(dir) == component {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}
