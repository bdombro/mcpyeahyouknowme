package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"mcpyeahyouknowme/core"

	"github.com/spf13/cobra"
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
		"status":          true,
		"completion":      true,
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
		"reset":           true,
	}

	root := newRootCmd()
	for _, cmd := range root.Commands() {
		if !cmd.IsAvailableCommand() {
			continue
		}
		name := cmd.Name()
		if !expected[name] {
			t.Errorf("unexpected command in list: %q", name)
		}
		delete(expected, name)
	}

	if len(expected) > 0 {
		t.Errorf("missing expected commands: %v", expected)
	}
}

// Verifies the reset command exists at the root level and calls resetAllRunner when executed.
func TestResetCommandCallsResetAllRunner(t *testing.T) {
	oldRunner := resetAllRunner
	defer func() { resetAllRunner = oldRunner }()

	called := false
	gotDir := ""
	resetAllRunner = func(dataDir string) {
		called = true
		gotDir = dataDir
	}

	root := newRootCmd()
	root.SetArgs([]string{"reset"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !called {
		t.Fatal("reset command did not call resetAllRunner")
	}
	if gotDir != core.DataDir() {
		t.Fatalf("reset command dataDir = %q, want %q", gotDir, core.DataDir())
	}
}

// Verifies the LaunchAgent label constant stays aligned with the installed plist name.
func TestPlistName(t *testing.T) {
	expectedName := "com.mcpyeahyouknowme.core"
	if plistName != expectedName {
		t.Errorf("plistName = %q, want %q", plistName, expectedName)
	}
}

// Verifies generated bash completions contain browser_history subcommands and chrome/brave choices.
func TestCompletionBash_ContainsBrowserHistoryOptions(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	if err := root.GenBashCompletion(&buf); err != nil {
		t.Fatalf("GenBashCompletion: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"browser_history",
		"chrome",
		"brave",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("bash completion missing %q", want)
		}
	}
}

// Verifies the browser_history enable subcommand advertises chrome and brave as valid args.
func TestCompletionZsh_ContainsBrowserHistoryOptions(t *testing.T) {
	root := newRootCmd()

	var bhCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "browser_history" {
			bhCmd = c
			break
		}
	}
	if bhCmd == nil {
		t.Fatal("browser_history command not found")
	}

	var enableCmd *cobra.Command
	for _, c := range bhCmd.Commands() {
		if c.Name() == "enable" {
			enableCmd = c
			break
		}
	}
	if enableCmd == nil {
		t.Fatal("browser_history enable command not found")
	}

	validSet := make(map[string]bool, len(enableCmd.ValidArgs))
	for _, v := range enableCmd.ValidArgs {
		validSet[v] = true
	}
	for _, want := range []string{"chrome", "brave"} {
		if !validSet[want] {
			t.Errorf("browser_history enable ValidArgs missing %q; got %v", want, enableCmd.ValidArgs)
		}
	}
}

// pathContains reports whether path contains component so plist-path assertions stay platform-safe.
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
