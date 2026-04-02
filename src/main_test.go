package main

import (
	"reflect"
	"testing"
)

// Verifies command dispatch returns the expected user-facing errors for missing or unknown commands.
func TestDispatchCommands(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"missing whatsapp subcommand", []string{"whatsapp"}, "Error: whatsapp subcommand required"},
		{"invalid whatsapp subcommand", []string{"whatsapp", "invalid"}, "Unknown whatsapp subcommand: invalid"},
		{"missing gsuite subcommand", []string{"gsuite"}, "Error: gsuite subcommand required"},
		{"invalid gsuite subcommand", []string{"gsuite", "invalid"}, "Unknown gsuite subcommand: invalid"},
		{"missing notebook subcommand", []string{"notebook"}, "Error: notebook subcommand required"},
		{"invalid notebook subcommand", []string{"notebook", "invalid"}, "Unknown notebook subcommand: invalid"},
		{"unknown command", []string{"unknown"}, "Unknown command: unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dispatchCommands(topLevelCommands(), tt.args); got != tt.wantErr {
				t.Errorf("dispatchCommands(%v) = %q, want %q", tt.args, got, tt.wantErr)
			}
		})
	}
}

// Verifies the root command-name list matches the expected public CLI commands.
func TestCommandNames(t *testing.T) {
	expected := []string{
		"mcp", "info", "completions", "core", "start", "stop",
		"restart", "reindex", "uninstall", "whatsapp", "gsuite", "notebook", "login", "reset",
	}
	if got := commandNames(topLevelCommands()); !reflect.DeepEqual(got, expected) {
		t.Errorf("commandNames() = %v, want %v", got, expected)
	}
}

// Verifies known subcommand groups still expose the expected subcommand names.
func TestSubcommandNames(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{"whatsapp", []string{"login", "reset"}},
		{"gsuite", []string{"login", "apps", "reset"}},
		{"notebook", []string{"add", "remove", "list", "reset"}},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			cmd := findCommand(topLevelCommands(), tt.command)
			if cmd == nil {
				t.Fatalf("command %q not found", tt.command)
			}
			if got := commandNames(cmd.Subcommands); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("subcommands for %s = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

// Verifies shell completion output still lists the supported shell arguments.
func TestShellCompletionWords(t *testing.T) {
	if got := shellCompletionWords(); got != "bash zsh" {
		t.Errorf("shellCompletionWords() = %q, want %q", got, "bash zsh")
	}
}
