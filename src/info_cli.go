package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/google_places"
	"mcpyeahyouknowme/sources/gsuite"
	"mcpyeahyouknowme/sources/registry"
	"mcpyeahyouknowme/sources/whatsapp"
)

func runInfo() {
	fmt.Print(renderInfo())
}

func renderInfo() string {
	dDir := core.DataDir()
	var b strings.Builder

	writeLine := func(format string, args ...interface{}) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	writeLine("┌──────────────────────────────────────────┐")
	writeLine("│         mcpyeahyouknowme info            │")
	writeLine("└──────────────────────────────────────────┘")
	writeLine("")

	writeLine("\U0001f527 Build")
	writeLine("   Version:    %s", BuildVersion)
	writeLine("   Built:      %s", BuildTime)
	writeLine("")

	writeLine("\u2699\ufe0f  Core Daemon")
	plist := plistPath()
	if _, err := os.Stat(plist); err == nil {
		ctxLC, cancelLC := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelLC()
		out, err := exec.CommandContext(ctxLC, "launchctl", "list", plistName).Output()
		if err == nil && len(out) > 0 {
			writeLine("   Status:     running")
		} else {
			writeLine("   Status:     installed (not running)")
		}
		writeLine("   Plist:      %s", plist)
		writeLine("   Logs:       %s", filepath.Join(dDir, "core.log"))
	} else {
		writeLine("   Status:     not installed")
	}
	if core.IsNetworkAvailable() {
		writeLine("   Network:    online")
	} else {
		writeLine("   Network:    offline (sync paused)")
	}
	writeLine("")

	writeLine("\U0001f4c1 Data")
	writeLine("   Directory:  %s", dDir)
	if info, err := os.Stat(dDir); err == nil && info.IsDir() {
		writeLine("   Status:     initialized")
	} else {
		writeLine("   Status:     not initialized (run 'mcpyeahyouknowme whatsapp login')")
	}
	writeLine("")

	writeSourceSection(&b, "\U0001f4f2 WhatsApp", "whatsapp", dDir, whatsapp.InfoLines)
	writeSourceSection(&b, "\U0001f537 Google Suite", "gsuite", dDir, gsuite.InfoLines)
	writeSourceSection(&b, "\U0001f4cd Google Places", "google_places", dDir, google_places.InfoLines)

	return b.String()
}

func writeSourceSection(b *strings.Builder, title, sourceName, dataDir string, infoLines func(string) []string) {
	fmt.Fprintln(b, title)
	if available, reason := registry.IsAvailable(sourceName); !available {
		fmt.Fprintln(b, "   Status:     unavailable")
		if reason != "" {
			fmt.Fprintf(b, "   Reason:     %s\n", reason)
		}
		fmt.Fprintln(b)
		return
	}
	for _, line := range infoLines(dataDir) {
		fmt.Fprintln(b, line)
	}
	fmt.Fprintln(b)
}
