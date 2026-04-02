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

// runInfo is the thin CLI entrypoint that writes the full status report to stdout for humans and startup logs.
func runInfo() {
	fmt.Print(renderInfo())
}

// renderInfo builds the info report string so CLI and daemon startup can show consistent status output.
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
	daemonRunning := false
	if _, err := os.Stat(plist); err == nil {
		ctxLC, cancelLC := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelLC()
		out, err := exec.CommandContext(ctxLC, "launchctl", "list", plistName).Output()
		if err == nil && len(out) > 0 {
			daemonRunning = true
			writeLine("   Status:     running")
			if rssBytes := daemonRSSBytes(plistName); rssBytes > 0 {
				writeLine("   RAM:        %s RSS", core.FormatSizeMB(rssBytes))
			}
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

	writeSearchIndexSection(&b, dDir, daemonRunning)

	writeSourceSection(&b, "\U0001f4f2 WhatsApp", "whatsapp", dDir, whatsapp.InfoLines)
	writeSourceSection(&b, "\U0001f537 Google Suite", "gsuite", dDir, gsuite.InfoLines)
	writeSourceSection(&b, "\U0001f4cd Google Places", "google_places", dDir, google_places.InfoLines)

	return b.String()
}

// writeSearchIndexSection appends search index status lines for dataDir, including size and indexing progress.
func writeSearchIndexSection(b *strings.Builder, dataDir string, daemonRunning bool) {
	fmt.Fprintln(b, "\U0001f50d Search Index")
	stats := ReadOnlySearchIndexStats(dataDir)
	if stats.Entries == 0 && stats.Embedded == 0 {
		fmt.Fprintln(b, "   Status:     not indexed (start daemon or run 'mcpyeahyouknowme reindex')")
		fmt.Fprintln(b)
		return
	}
	pct := 0
	if stats.Entries > 0 {
		pct = stats.Embedded * 100 / stats.Entries
	}
	fmt.Fprintf(b, "   Entries:    %d\n", stats.Entries)
	fmt.Fprintf(b, "   Indexed:    %d (%d%%)\n", stats.Embedded, pct)
	if sizeBytes := core.FileGroupSizeBytes(filepath.Join(dataDir, "search.db")); sizeBytes > 0 {
		fmt.Fprintf(b, "   DB Size:    %s\n", core.FormatSizeMB(sizeBytes))
	}
	if stats.Embedded < stats.Entries {
		if daemonRunning {
			fmt.Fprintln(b, "   Status:     indexing in progress")
		} else {
			fmt.Fprintln(b, "   Status:     daemon not running")
		}
	}
	fmt.Fprintln(b)
}

// writeSourceSection appends one source status block, including availability or source-specific info lines.
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
