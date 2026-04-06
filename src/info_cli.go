package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/brave_search"
	"mcpyeahyouknowme/sources/browser_history"
	"mcpyeahyouknowme/sources/google_places"
	"mcpyeahyouknowme/sources/gsuite"
	"mcpyeahyouknowme/sources/notebook"
	"mcpyeahyouknowme/sources/registry"
	"mcpyeahyouknowme/sources/whatsapp"
)

type infoBuildSnapshot struct {
	Version string `json:"version"`
	Built   string `json:"built"`
}

type infoCoreDaemonSnapshot struct {
	Network   string `json:"network"`
	Status    string `json:"status"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Plist     string `json:"plist,omitempty"`
	Logs      string `json:"logs,omitempty"`
	RAM       string `json:"ram,omitempty"`
}

type infoDataSnapshot struct {
	Directory   string `json:"directory"`
	Status      string `json:"status"`
	Initialized bool   `json:"initialized"`
}

type infoSearchIndexSnapshot struct {
	Entries int    `json:"entries"`
	DBSize  string `json:"db_size,omitempty"`
	Status  string `json:"status,omitempty"`
}

type infoSourceSnapshot struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Lines     []string `json:"lines"`
}

type infoSnapshot struct {
	Build       infoBuildSnapshot       `json:"build"`
	CoreDaemon  infoCoreDaemonSnapshot  `json:"core_daemon"`
	Data        infoDataSnapshot        `json:"data"`
	SearchIndex infoSearchIndexSnapshot `json:"search_index"`
	Sources     []infoSourceSnapshot    `json:"sources"`
}

type infoSourceDef struct {
	Title     string
	Key       string
	InfoLines func(string) []string
}

type statusOptions struct {
	jsonOutput bool
	live       bool
}

var infoDataDir = core.DataDir
var infoFileGroupSizeBytes = core.FileGroupSizeBytes
var infoIsNetworkAvailable = core.IsNetworkAvailable
var infoSearchIndexStats = ReadOnlySearchIndexStats
var infoSourceAvailability = registry.IsAvailable
var infoPlistPath = plistPath
var infoDaemonRSSBytes = daemonRSSBytes
var infoLaunchctlOutput = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "launchctl", "list", plistName).Output()
}
var infoStat = os.Stat
var infoSourceDefs = []infoSourceDef{
	{Title: "\U0001f50d Brave Search", Key: "brave_search", InfoLines: brave_search.InfoLines},
	{Title: "\U0001f5c2\ufe0f Browser History", Key: "browser_history", InfoLines: browser_history.InfoLines},
	{Title: "\U0001f4cd Google Places", Key: "google_places", InfoLines: google_places.InfoLines},
	{Title: "\U0001f537 Google Suite", Key: "gsuite", InfoLines: gsuite.InfoLines},
	{Title: "\U0001f4dd Notebook", Key: "notebook", InfoLines: notebook.InfoLines},
	{Title: "\U0001f4f2 WhatsApp", Key: "whatsapp", InfoLines: whatsapp.InfoLines},
}
var statusBuildSnapshot = buildInfoSnapshot
var statusMarshalIndent = json.MarshalIndent
var statusStdout io.Writer = os.Stdout
var statusStderr io.Writer = os.Stderr
var statusExit = os.Exit
var statusLiveInterval = 10 * time.Second
var statusNotifyContext = signal.NotifyContext
var statusTicker = func(interval time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

const statusRedrawPrefix = "\x1b[H\x1b[2J"

// runStatus routes the human, JSON, or live status report to stdout and exits non-zero when callers pass unsupported flags.
func runStatus(args []string) {
	if err := writeStatus(statusStdout, args); err != nil {
		fmt.Fprintf(statusStderr, "Error: %v\n", err)
		statusExit(1)
	}
}

// writeStatus renders the shared status snapshot as text by default, as JSON for --json, or as a live view for --live.
func writeStatus(w io.Writer, args []string) error {
	opts, err := parseStatusArgs(args)
	if err != nil {
		return err
	}
	if opts.live {
		return writeStatusLive(w)
	}

	snapshot := statusBuildSnapshot()
	if opts.jsonOutput {
		data, err := statusMarshalIndent(snapshot, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal status json: %w", err)
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}
	_, err = io.WriteString(w, renderStatusSnapshot(snapshot))
	return err
}

// parseStatusArgs recognizes the small manual flag surface for status and rejects unsupported combinations or unknown flags.
func parseStatusArgs(args []string) (statusOptions, error) {
	opts := statusOptions{}
	for _, arg := range args {
		switch arg {
		case "--json":
			opts.jsonOutput = true
		case "--live":
			opts.live = true
		default:
			return statusOptions{}, fmt.Errorf("unsupported status argument %q", arg)
		}
	}
	if opts.live && opts.jsonOutput {
		return statusOptions{}, fmt.Errorf("status --live cannot be combined with --json")
	}
	return opts, nil
}

// writeStatusLive refreshes the human-readable status view in place until the user interrupts the process.
func writeStatusLive(w io.Writer) error {
	ctx, stop := statusNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return writeStatusLiveWithContext(ctx, w)
}

// writeStatusLiveWithContext redraws the current status output on a fixed interval until ctx is canceled.
func writeStatusLiveWithContext(ctx context.Context, w io.Writer) error {
	if err := writeStatusFrame(w); err != nil {
		return err
	}

	ticks, stop := statusTicker(statusLiveInterval)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			if err := writeStatusFrame(w); err != nil {
				return err
			}
		}
	}
}

// writeStatusFrame redraws one fresh human-readable status report by clearing the terminal and writing the current snapshot.
func writeStatusFrame(w io.Writer) error {
	_, err := io.WriteString(w, statusRedrawPrefix+renderStatusSnapshot(statusBuildSnapshot()))
	return err
}

// renderStatus builds the status report string so CLI callers and tests share the same human-readable format.
func renderStatus() string {
	return renderStatusSnapshot(statusBuildSnapshot())
}

// renderStatusSnapshot formats a precomputed status snapshot as the existing human-readable report.
func renderStatusSnapshot(snapshot infoSnapshot) string {
	var b strings.Builder

	writeLine := func(format string, args ...interface{}) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	writeLine("┌──────────────────────────────────────────┐")
	writeLine("│        mcpyeahyouknowme status           │")
	writeLine("└──────────────────────────────────────────┘")
	writeLine("")

	writeLine("\U0001f527 Build")
	writeLine("   Version:    %s", snapshot.Build.Version)
	writeLine("   Built:      %s", snapshot.Build.Built)
	writeLine("")

	writeLine("\u2699\ufe0f  Core Daemon")
	writeLine("   Network:    %s", snapshot.CoreDaemon.Network)
	writeLine("   Status:     %s", snapshot.CoreDaemon.Status)
	if snapshot.CoreDaemon.Plist != "" {
		writeLine("   Plist:      %s", snapshot.CoreDaemon.Plist)
	}
	if snapshot.CoreDaemon.Logs != "" {
		writeLine("   Logs:       %s", snapshot.CoreDaemon.Logs)
	}
	if snapshot.CoreDaemon.RAM != "" {
		writeLine("   RAM:        %s", snapshot.CoreDaemon.RAM)
	}
	writeLine("")

	writeLine("\U0001f4c1 Data")
	writeLine("   Directory:  %s", snapshot.Data.Directory)
	writeLine("   Status:     %s", snapshot.Data.Status)
	writeLine("")

	writeSearchIndexSection(&b, snapshot.SearchIndex)
	for _, source := range snapshot.Sources {
		writeSourceSection(&b, source)
	}
	return b.String()
}

// buildInfoSnapshot gathers the status fields once so text and JSON output stay consistent.
func buildInfoSnapshot() infoSnapshot {
	dataDir := infoDataDir()
	coreDaemon := buildInfoCoreDaemonSnapshot(dataDir)
	return infoSnapshot{
		Build: infoBuildSnapshot{
			Version: BuildVersion,
			Built:   BuildTime,
		},
		CoreDaemon:  coreDaemon,
		Data:        buildInfoDataSnapshot(dataDir),
		SearchIndex: buildInfoSearchIndexSnapshot(dataDir, coreDaemon.Running),
		Sources:     buildInfoSourceSnapshots(dataDir),
	}
}

// buildInfoCoreDaemonSnapshot captures daemon install and runtime state without writing any CLI output.
func buildInfoCoreDaemonSnapshot(dataDir string) infoCoreDaemonSnapshot {
	snapshot := infoCoreDaemonSnapshot{
		Network: "offline (sync paused)",
		Status:  "not installed",
	}
	if infoIsNetworkAvailable() {
		snapshot.Network = "online"
	}

	plist := infoPlistPath()
	if _, err := infoStat(plist); err != nil {
		return snapshot
	}

	snapshot.Installed = true
	snapshot.Status = "installed (not running)"
	snapshot.Plist = plist
	snapshot.Logs = filepath.Join(dataDir, "core.log")

	ctxLC, cancelLC := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLC()
	out, err := infoLaunchctlOutput(ctxLC)
	if err != nil || len(out) == 0 {
		return snapshot
	}

	snapshot.Running = true
	snapshot.Status = "running"
	if rssBytes := infoDaemonRSSBytes(plistName); rssBytes > 0 {
		snapshot.RAM = fmt.Sprintf("%s RSS", core.FormatSizeMB(rssBytes))
	}
	return snapshot
}

// buildInfoDataSnapshot records whether the shared data directory already exists for the current install.
func buildInfoDataSnapshot(dataDir string) infoDataSnapshot {
	snapshot := infoDataSnapshot{
		Directory:   dataDir,
		Status:      "not initialized (run 'mcpyeahyouknowme whatsapp login')",
		Initialized: false,
	}
	if fileInfo, err := infoStat(dataDir); err == nil && fileInfo.IsDir() {
		snapshot.Status = "initialized"
		snapshot.Initialized = true
	}
	return snapshot
}

// buildInfoSearchIndexSnapshot summarizes read-only search index stats for both CLI formats.
func buildInfoSearchIndexSnapshot(dataDir string, _ bool) infoSearchIndexSnapshot {
	stats := infoSearchIndexStats(dataDir)
	snapshot := infoSearchIndexSnapshot{
		Entries: stats.Entries,
	}
	if stats.Entries == 0 {
		snapshot.Status = "not indexed"
		return snapshot
	}
	if sizeBytes := infoFileGroupSizeBytes(filepath.Join(dataDir, "search.db")); sizeBytes > 0 {
		snapshot.DBSize = core.FormatSizeMB(sizeBytes)
	}
	return snapshot
}

// buildInfoSourceSnapshots keeps source ordering stable while attaching availability metadata to each section.
func buildInfoSourceSnapshots(dataDir string) []infoSourceSnapshot {
	sources := make([]infoSourceSnapshot, 0, len(infoSourceDefs))
	for _, def := range infoSourceDefs {
		source := infoSourceSnapshot{
			Key:       def.Key,
			Title:     def.Title,
			Available: true,
		}
		if available, reason := infoSourceAvailability(def.Key); !available {
			source.Available = false
			source.Reason = reason
			sources = append(sources, source)
			continue
		}
		source.Lines = def.InfoLines(dataDir)
		sources = append(sources, source)
	}
	return sources
}

// writeSearchIndexSection appends search index status lines from a precomputed snapshot to the human report.
func writeSearchIndexSection(b *strings.Builder, snapshot infoSearchIndexSnapshot) {
	fmt.Fprintln(b, "\U0001f50d Search Index")
	if snapshot.Entries == 0 {
		fmt.Fprintln(b, "   Status:     not indexed")
		fmt.Fprintln(b)
		return
	}
	fmt.Fprintf(b, "   Entries:    %d\n", snapshot.Entries)
	if snapshot.DBSize != "" {
		fmt.Fprintf(b, "   DB Size:    %s\n", snapshot.DBSize)
	}
	if snapshot.Status != "" {
		fmt.Fprintf(b, "   Status:     %s\n", snapshot.Status)
	}
	fmt.Fprintln(b)
}

// writeSourceSection appends one source status block, including availability or source-specific info lines.
func writeSourceSection(b *strings.Builder, snapshot infoSourceSnapshot) {
	fmt.Fprintln(b, snapshot.Title)
	if !snapshot.Available {
		fmt.Fprintln(b, "   Status:     unavailable")
		if snapshot.Reason != "" {
			fmt.Fprintf(b, "   Reason:     %s\n", snapshot.Reason)
		}
		fmt.Fprintln(b)
		return
	}
	for _, line := range snapshot.Lines {
		fmt.Fprintln(b, line)
	}
	fmt.Fprintln(b)
}
