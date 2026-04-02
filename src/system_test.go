package main

import "testing"

func TestParseLaunchctlPID(t *testing.T) {
	output := `{
	"Label" = "com.mcpyeahyouknowme.core";
	"PID" = 51454;
}`
	if got := parseLaunchctlPID(output); got != 51454 {
		t.Fatalf("parseLaunchctlPID() = %d, want 51454", got)
	}
}

func TestParseLaunchctlPID_missing(t *testing.T) {
	if got := parseLaunchctlPID(`{"Label" = "com.mcpyeahyouknowme.core";}`); got != 0 {
		t.Fatalf("parseLaunchctlPID() = %d, want 0", got)
	}
}

func TestParseProcessRSSBytes(t *testing.T) {
	if got := parseProcessRSSBytes("896384\n"); got != 917897216 {
		t.Fatalf("parseProcessRSSBytes() = %d, want 917897216", got)
	}
}

func TestParseProcessRSSBytes_invalid(t *testing.T) {
	if got := parseProcessRSSBytes("not-a-number"); got != 0 {
		t.Fatalf("parseProcessRSSBytes() = %d, want 0", got)
	}
}
