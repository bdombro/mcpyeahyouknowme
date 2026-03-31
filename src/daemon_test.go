package main

import (
	"os"
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

func TestWhatsAppReset_noDataDir(t *testing.T) {
	dDir := filepath.Join(t.TempDir(), "nonexistent")
	plist := filepath.Join(t.TempDir(), "fake.plist")

	// Should return early without error when data dir doesn't exist
	whatsAppReset(dDir, plist)
}

func TestWhatsAppReset_removesOnlyWhatsAppFiles(t *testing.T) {
	dDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "fake.plist") // no plist → daemon not installed

	waDB := filepath.Join(dDir, "whatsapp.db")
	msgDB := filepath.Join(dDir, "messages.db")
	gdDB := filepath.Join(dDir, "googledocs.db")
	token := filepath.Join(dDir, "googledocs_token.json")

	for _, f := range []string{waDB, msgDB, gdDB, token} {
		if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	whatsAppReset(dDir, plist)

	// WhatsApp files should be gone
	for _, f := range []string{waDB, msgDB} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", filepath.Base(f))
		}
	}
	// Google Docs files should be preserved
	for _, f := range []string{gdDB, token} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected %s to be preserved, got err: %v", filepath.Base(f), err)
		}
	}
}

func TestWhatsAppReset_preservesPlist(t *testing.T) {
	dDir := t.TempDir()
	plistDir := t.TempDir()
	plist := filepath.Join(plistDir, "com.test.plist")

	// Create plist file to simulate installed daemon
	if err := os.WriteFile(plist, []byte("<plist/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	whatsAppReset(dDir, plist)

	// Plist must NOT be deleted — daemon installation should survive reset
	if _, err := os.Stat(plist); os.IsNotExist(err) {
		t.Error("plist file should be preserved after whatsapp reset")
	}
}

func TestWhatsAppReset_toleratesMissingFiles(t *testing.T) {
	dDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "fake.plist")

	// Data dir exists but contains no WhatsApp files — should not error
	whatsAppReset(dDir, plist)
}
