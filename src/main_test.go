package main

import (
	"reflect"
	"testing"
)

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

func TestCommandNames(t *testing.T) {
	expected := []string{
		"mcp", "info", "completions", "core", "start", "stop",
		"restart", "reindex", "uninstall", "whatsapp", "gsuite", "login", "reset",
	}
	if got := commandNames(topLevelCommands()); !reflect.DeepEqual(got, expected) {
		t.Errorf("commandNames() = %v, want %v", got, expected)
	}
}

func TestSubcommandNames(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{"whatsapp", []string{"login", "reset"}},
		{"gsuite", []string{"login", "apps", "reset"}},
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

func TestShellCompletionWords(t *testing.T) {
	if got := shellCompletionWords(); got != "bash zsh" {
		t.Errorf("shellCompletionWords() = %q, want %q", got, "bash zsh")
	}
}
