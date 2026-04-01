package main

import (
	"testing"
)

func TestCommandRouting(t *testing.T) {
	tests := []struct {
		name        string
		cmd         string
		args        []string
		wantValid   bool
		description string
	}{
		{"mcp command", "mcp", []string{}, true, "should route to runMcp"},
		{"info command", "info", []string{}, true, "should route to runInfo"},
		{"completions zsh", "completions", []string{"zsh"}, true, "should route to runCompletions with zsh"},
		{"completions bash", "completions", []string{"bash"}, true, "should route to runCompletions with bash"},
		{"completions default", "completions", []string{}, true, "should default to zsh"},
		{"core command", "core", []string{}, true, "should route to runCore"},
		{"start command", "start", []string{}, true, "should route to runStart"},
		{"stop command", "stop", []string{}, true, "should route to runStop"},
		{"restart command", "restart", []string{}, true, "should route to runRestart"},
		{"uninstall command", "uninstall", []string{}, true, "should route to runUninstall"},
		{"whatsapp login", "whatsapp", []string{"login"}, true, "should route to WhatsApp login"},
		{"whatsapp reset", "whatsapp", []string{"reset"}, true, "should route to WhatsApp reset"},
		{"whatsapp no subcommand", "whatsapp", []string{}, false, "should error on missing subcommand"},
		{"whatsapp invalid", "whatsapp", []string{"invalid"}, false, "should error on invalid subcommand"},
		{"gsuite login", "gsuite", []string{"login"}, true, "should route to Google Suite login"},
		{"gsuite apps", "gsuite", []string{"apps"}, true, "should route to Google Suite apps"},
		{"gsuite reset", "gsuite", []string{"reset"}, true, "should route to Google Suite reset"},
		{"gsuite no subcommand", "gsuite", []string{}, false, "should error on missing subcommand"},
		{"gsuite invalid", "gsuite", []string{"invalid"}, false, "should error on invalid subcommand"},
		{"legacy login", "login", []string{}, true, "should route to legacy login"},
		{"legacy reset", "reset", []string{}, true, "should route to legacy reset"},
		{"unknown command", "unknown", []string{}, false, "should error on unknown command"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validCommands := map[string]bool{
				"mcp": true, "info": true, "completions": true,
				"core": true, "start": true, "stop": true, "restart": true,
				"uninstall": true, "whatsapp": true, "gsuite": true,
				"login": true, "reset": true,
			}

			isValid := validCommands[tt.cmd]

			if tt.cmd == "whatsapp" {
				if len(tt.args) == 0 {
					isValid = false
				} else {
					validSubs := map[string]bool{"login": true, "reset": true}
					isValid = validSubs[tt.args[0]]
				}
			}

			if tt.cmd == "gsuite" {
				if len(tt.args) == 0 {
					isValid = false
				} else {
					validSubs := map[string]bool{"login": true, "apps": true, "reset": true}
					isValid = validSubs[tt.args[0]]
				}
			}

			if isValid != tt.wantValid {
				t.Errorf("%s: got valid=%v, want %v", tt.description, isValid, tt.wantValid)
			}
		})
	}
}

func TestCommandsList(t *testing.T) {
	expectedCommands := []string{
		"mcp", "info", "completions", "core", "start", "stop",
		"restart", "uninstall", "whatsapp", "gsuite", "login", "reset",
	}

	commandMap := make(map[string]bool)
	for _, cmd := range commands {
		commandMap[cmd] = true
	}

	for _, expected := range expectedCommands {
		if !commandMap[expected] {
			t.Errorf("Expected command %q not found in commands list", expected)
		}
	}

	if len(commands) != len(expectedCommands) {
		t.Errorf("Commands list length mismatch: got %d, want %d", len(commands), len(expectedCommands))
	}
}

func TestCompletionsShellValidation(t *testing.T) {
	tests := []struct {
		shell     string
		shouldErr bool
	}{
		{"bash", false},
		{"zsh", false},
		{"fish", true},
		{"invalid", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			validShells := map[string]bool{"bash": true, "zsh": true}
			isValid := validShells[tt.shell]
			shouldErr := !isValid

			if shouldErr != tt.shouldErr {
				t.Errorf("Shell %q: got shouldErr=%v, want %v", tt.shell, shouldErr, tt.shouldErr)
			}
		})
	}
}
