package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Verifies the Cobra root command rejects unknown top-level commands.
func TestDispatchCLI_unknownCommand(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"doesnotexist"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
}

// Verifies group commands show help (not an error) when invoked without a subcommand —
// this is standard Cobra behavior and is more user-friendly than a bare error.
func TestDispatchCLI_groupCommandShowsHelp(t *testing.T) {
	groups := []string{"whatsapp", "gsuite", "browser_history", "notebook"}
	for _, g := range groups {
		t.Run(g, func(t *testing.T) {
			root := newRootCmd()
			var sb strings.Builder
			root.SetOut(&sb)
			root.SetErr(&sb)
			root.SetArgs([]string{g})
			err := root.Execute()
			if err != nil {
				t.Errorf("%s: expected nil error for bare group command, got %v", g, err)
			}
			if !strings.Contains(sb.String(), "Available Commands") {
				t.Errorf("%s: expected help output, got %q", g, sb.String())
			}
		})
	}
}

// Verifies the root command exposes the expected set of public top-level commands.
func TestCommandNames(t *testing.T) {
	expected := map[string]bool{
		"mcp":             true,
		"status":          true,
		"completion":      true,
		"core":            true,
		"start":           true,
		"stop":            true,
		"restart":         true,
		"reindex":         true,
		"reset":           true,
		"uninstall":       true,
		"whatsapp":        true,
		"gsuite":          true,
		"browser_history": true,
		"notebook":        true,
	}
	root := newRootCmd()
	publicCmds := 0
	for _, cmd := range root.Commands() {
		if !cmd.IsAvailableCommand() {
			continue
		}
		publicCmds++
		if !expected[cmd.Name()] {
			t.Errorf("unexpected command: %q", cmd.Name())
		}
	}
	if publicCmds != len(expected) {
		t.Errorf("expected %d public commands, got %d", len(expected), publicCmds)
	}
}

// Verifies each command group exposes the expected subcommands.
func TestSubcommandNames(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{"completion", []string{"bash", "zsh"}},
		{"whatsapp", []string{"enable", "disable", "login", "reset"}},
		{"gsuite", []string{"enable", "disable", "login", "manage", "reset"}},
		{"browser_history", []string{"enable", "disable", "reset"}},
		{"notebook", []string{"enable", "disable", "add", "remove", "list", "reset"}},
	}
	root := newRootCmd()
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			var parent *cobra.Command
			for _, c := range root.Commands() {
				if c.Name() == tt.command {
					parent = c
					break
				}
			}
			if parent == nil {
				t.Fatalf("command %q not found in root", tt.command)
			}
			got := make([]string, 0, len(parent.Commands()))
			for _, sub := range parent.Commands() {
				if sub.IsAvailableCommand() {
					got = append(got, sub.Name())
				}
			}
			gotSet := make(map[string]bool, len(got))
			for _, n := range got {
				gotSet[n] = true
			}
			for _, want := range tt.want {
				if !gotSet[want] {
					t.Errorf("%s: missing subcommand %q; got %v", tt.command, want, got)
				}
			}
		})
	}
}

// Verifies the Cobra completion command generates non-empty output for bash and zsh.
func TestCompletionShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			root := newRootCmd()
			var sb strings.Builder
			root.SetOut(&sb)
			root.SetArgs([]string{"completion", shell})
			if err := root.Execute(); err != nil {
				t.Fatalf("completion %s: %v", shell, err)
			}
			if sb.Len() == 0 {
				t.Errorf("completion %s returned empty output", shell)
			}
		})
	}
}
