package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type errWriter struct{}

// Returns a stable write error so live-mode tests can cover the immediate writer-failure branch deterministically.
func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type failAfterWriter struct {
	writes int
}

// Returns one successful write before failing so live-mode tests can cover redraw errors after the first frame.
func (w *failAfterWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		return 0, errors.New("write failed")
	}
	return len(p), nil
}

// restoreStatusTestGlobals resets the status CLI test hooks after each test so stubs never leak across cases.
func restoreStatusTestGlobals(t *testing.T) func() {
	t.Helper()

	oldDataDir := infoDataDir
	oldFileGroupSizeBytes := infoFileGroupSizeBytes
	oldIsNetworkAvailable := infoIsNetworkAvailable
	oldSearchIndexStats := infoSearchIndexStats
	oldSourceAvailability := infoSourceAvailability
	oldPlistPath := infoPlistPath
	oldDaemonRSSBytes := infoDaemonRSSBytes
	oldLaunchctlOutput := infoLaunchctlOutput
	oldStat := infoStat
	oldSourceDefs := infoSourceDefs
	oldBuildSnapshot := statusBuildSnapshot
	oldMarshalIndent := statusMarshalIndent
	oldStdout := statusStdout
	oldStderr := statusStderr
	oldExit := statusExit
	oldLiveInterval := statusLiveInterval
	oldNotifyContext := statusNotifyContext
	oldTicker := statusTicker
	oldVersion := BuildVersion
	oldTime := BuildTime

	return func() {
		infoDataDir = oldDataDir
		infoFileGroupSizeBytes = oldFileGroupSizeBytes
		infoIsNetworkAvailable = oldIsNetworkAvailable
		infoSearchIndexStats = oldSearchIndexStats
		infoSourceAvailability = oldSourceAvailability
		infoPlistPath = oldPlistPath
		infoDaemonRSSBytes = oldDaemonRSSBytes
		infoLaunchctlOutput = oldLaunchctlOutput
		infoStat = oldStat
		infoSourceDefs = oldSourceDefs
		statusBuildSnapshot = oldBuildSnapshot
		statusMarshalIndent = oldMarshalIndent
		statusStdout = oldStdout
		statusStderr = oldStderr
		statusExit = oldExit
		statusLiveInterval = oldLiveInterval
		statusNotifyContext = oldNotifyContext
		statusTicker = oldTicker
		BuildVersion = oldVersion
		BuildTime = oldTime
	}
}

// Verifies runStatus writes a human-readable report that still ends with the expected trailing blank line.
func TestRunStatus_EndsWithBlankLine(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	var stdout bytes.Buffer
	statusStdout = &stdout
	statusStderr = &bytes.Buffer{}
	statusExit = func(int) {
		t.Fatal("runStatus should not exit for the default text path")
	}

	BuildVersion = "test-version"
	BuildTime = "test-time"

	runStatus(nil)

	got := stdout.String()
	if !strings.Contains(got, "mcpyeahyouknowme status") {
		t.Fatalf("expected status header in output, got %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("expected output to end with a blank line, got suffix %q", got[max(0, len(got)-4):])
	}
}

// Verifies runStatus surfaces unsupported arguments on stderr and exits with a non-zero status.
func TestRunStatus_invalidArgExits(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := 0
	statusStdout = &stdout
	statusStderr = &stderr
	statusExit = func(code int) {
		exitCode = code
	}

	runStatus([]string{"--bogus"})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout on error, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unsupported status argument "--bogus"`) {
		t.Fatalf("expected unsupported-arg error, got %q", stderr.String())
	}
}

// Verifies parseStatusArgs enables live mode and leaves JSON disabled when callers pass only --live.
func TestParseStatusArgs_live(t *testing.T) {
	opts, err := parseStatusArgs([]string{"--live"})
	if err != nil {
		t.Fatalf("parseStatusArgs: %v", err)
	}
	if !opts.live || opts.jsonOutput {
		t.Fatalf("expected live-only options, got %#v", opts)
	}
}

// Verifies parseStatusArgs rejects the mutually exclusive live and JSON modes with a clear error.
func TestParseStatusArgs_rejectsLiveJSON(t *testing.T) {
	_, err := parseStatusArgs([]string{"--live", "--json"})
	if err == nil || !strings.Contains(err.Error(), "status --live cannot be combined with --json") {
		t.Fatalf("expected live/json validation error, got %v", err)
	}
}

// Verifies writeStatus emits pretty JSON with the expected top-level status sections when callers pass --json.
func TestWriteStatus_json(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	dataDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "test.plist")
	if err := os.WriteFile(plist, []byte("plist"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	BuildVersion = "json-version"
	BuildTime = "json-time"
	infoDataDir = func() string { return dataDir }
	infoPlistPath = func() string { return plist }
	infoIsNetworkAvailable = func() bool { return true }
	infoLaunchctlOutput = func(context.Context) ([]byte, error) { return []byte("123"), nil }
	infoDaemonRSSBytes = func(string) int64 { return 2 * 1024 * 1024 }
	infoSearchIndexStats = func(string) SearchIndexStats { return SearchIndexStats{Entries: 4} }
	infoFileGroupSizeBytes = func(string) int64 { return 3 * 1024 * 1024 }
	infoSourceDefs = []infoSourceDef{
		{
			Title: "Alpha",
			Key:   "alpha",
			InfoLines: func(string) []string {
				return []string{"   Status:     enabled"}
			},
		},
	}
	infoSourceAvailability = func(string) (bool, string) { return true, "" }

	var stdout bytes.Buffer
	if err := writeStatus(&stdout, []string{"--json"}); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}

	var got infoSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal status json: %v\noutput: %s", err, stdout.String())
	}
	if got.Build.Version != "json-version" {
		t.Fatalf("expected build version in JSON, got %#v", got.Build)
	}
	if got.CoreDaemon.Status != "running" {
		t.Fatalf("expected running daemon status, got %#v", got.CoreDaemon)
	}
	if got.SearchIndex.Entries != 4 {
		t.Fatalf("expected entries 4, got %#v", got.SearchIndex)
	}
	if len(got.Sources) != 1 || got.Sources[0].Key != "alpha" {
		t.Fatalf("expected alpha source in JSON, got %#v", got.Sources)
	}
}

// Verifies writeStatus returns a wrapped error when JSON marshaling fails so callers can surface the failure cleanly.
func TestWriteStatus_jsonMarshalError(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	statusMarshalIndent = func(any, string, string) ([]byte, error) {
		return nil, errors.New("boom")
	}

	err := writeStatus(&bytes.Buffer{}, []string{"--json"})
	if err == nil || !strings.Contains(err.Error(), "marshal status json: boom") {
		t.Fatalf("expected wrapped marshal error, got %v", err)
	}
}

// Verifies writeStatus routes --live through the redraw loop instead of rendering the one-shot text or JSON outputs.
func TestWriteStatus_live(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	statusNotifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	statusBuildSnapshot = func() infoSnapshot {
		return infoSnapshot{
			Build:      infoBuildSnapshot{Version: "live-version", Built: "now"},
			CoreDaemon: infoCoreDaemonSnapshot{Network: "online", Status: "running"},
			Data:       infoDataSnapshot{Directory: "/tmp/data", Status: "initialized", Initialized: true},
		}
	}

	var stdout bytes.Buffer
	if err := writeStatus(&stdout, []string{"--live"}); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}

	got := stdout.String()
	if !strings.HasPrefix(got, statusRedrawPrefix) || !strings.Contains(got, "live-version") {
		t.Fatalf("expected live redraw output, got %q", got)
	}
}

// Verifies writeStatusLive returns after drawing one frame when the notify context is already canceled.
func TestWriteStatusLive_canceledContext(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	statusNotifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	statusBuildSnapshot = func() infoSnapshot {
		return infoSnapshot{
			Build:      infoBuildSnapshot{Version: "live-version", Built: "now"},
			CoreDaemon: infoCoreDaemonSnapshot{Network: "online", Status: "running"},
			Data:       infoDataSnapshot{Directory: "/tmp/data", Status: "initialized", Initialized: true},
		}
	}

	var stdout bytes.Buffer
	if err := writeStatusLive(&stdout); err != nil {
		t.Fatalf("writeStatusLive: %v", err)
	}
	if !strings.Contains(stdout.String(), "live-version") {
		t.Fatalf("expected live frame in output, got %q", stdout.String())
	}
}

// Verifies writeStatusLiveWithContext returns the first frame error immediately instead of entering the ticker loop.
func TestWriteStatusLiveWithContext_initialWriteError(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	statusBuildSnapshot = func() infoSnapshot {
		return infoSnapshot{
			Build:      infoBuildSnapshot{Version: "live-version", Built: "now"},
			CoreDaemon: infoCoreDaemonSnapshot{Network: "online", Status: "running"},
			Data:       infoDataSnapshot{Directory: "/tmp/data", Status: "initialized", Initialized: true},
		}
	}

	err := writeStatusLiveWithContext(context.Background(), errWriter{})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected initial write failure, got %v", err)
	}
}

// Verifies writeStatusLiveWithContext redraws at least one refreshed frame before the context is canceled.
func TestWriteStatusLiveWithContext_redrawsOnTick(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	tickC := make(chan time.Time, 1)
	statusTicker = func(time.Duration) (<-chan time.Time, func()) {
		return tickC, func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	buildCalls := 0
	statusBuildSnapshot = func() infoSnapshot {
		buildCalls++
		if buildCalls == 2 {
			cancel()
		}
		return infoSnapshot{
			Build: infoBuildSnapshot{
				Version: fmt.Sprintf("version-%d", buildCalls),
				Built:   "now",
			},
			CoreDaemon: infoCoreDaemonSnapshot{Network: "online", Status: "running"},
			Data:       infoDataSnapshot{Directory: "/tmp/data", Status: "initialized", Initialized: true},
		}
	}

	var stdout bytes.Buffer
	tickC <- time.Now()
	if err := writeStatusLiveWithContext(ctx, &stdout); err != nil {
		t.Fatalf("writeStatusLiveWithContext: %v", err)
	}

	got := stdout.String()
	if strings.Count(got, statusRedrawPrefix) != 2 {
		t.Fatalf("expected two redraw frames, got %q", got)
	}
	if !strings.Contains(got, "version-1") || !strings.Contains(got, "version-2") {
		t.Fatalf("expected both rendered frames in output, got %q", got)
	}
}

// Verifies writeStatusLiveWithContext surfaces redraw errors that happen after the first successful frame.
func TestWriteStatusLiveWithContext_tickWriteError(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	tickC := make(chan time.Time, 1)
	statusTicker = func(time.Duration) (<-chan time.Time, func()) {
		return tickC, func() {}
	}
	statusBuildSnapshot = func() infoSnapshot {
		return infoSnapshot{
			Build:      infoBuildSnapshot{Version: "live-version", Built: "now"},
			CoreDaemon: infoCoreDaemonSnapshot{Network: "online", Status: "running"},
			Data:       infoDataSnapshot{Directory: "/tmp/data", Status: "initialized", Initialized: true},
		}
	}

	tickC <- time.Now()
	err := writeStatusLiveWithContext(context.Background(), &failAfterWriter{})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected tick write failure, got %v", err)
	}
}

// Verifies renderStatus delegates to the shared snapshot builder and still includes the Search Index section header.
func TestRenderStatus_containsSearchIndex(t *testing.T) {
	got := renderStatus()
	if !strings.Contains(got, "Search Index") {
		t.Fatalf("expected Search Index section in output, got %q", got)
	}
}

// Verifies renderStatusSnapshot prints optional daemon fields plus both available and unavailable source sections.
func TestRenderStatusSnapshot_rendersOptionalFields(t *testing.T) {
	snapshot := infoSnapshot{
		Build: infoBuildSnapshot{Version: "v1", Built: "now"},
		CoreDaemon: infoCoreDaemonSnapshot{
			Network: "online",
			Status:  "running",
			Plist:   "/tmp/test.plist",
			Logs:    "/tmp/core.log",
			RAM:     "2.0 MB RSS",
		},
		Data: infoDataSnapshot{
			Directory:   "/tmp/data",
			Status:      "initialized",
			Initialized: true,
		},
		SearchIndex: infoSearchIndexSnapshot{
			Entries: 4,
			DBSize:  "3.0 MB",
		},
		Sources: []infoSourceSnapshot{
			{Title: "Alpha", Available: true, Lines: []string{"   Status:     enabled"}},
			{Title: "Beta", Available: false, Reason: "missing credentials"},
		},
	}

	got := renderStatusSnapshot(snapshot)
	for _, want := range []string{
		"mcpyeahyouknowme status",
		"Plist:      /tmp/test.plist",
		"Logs:       /tmp/core.log",
		"RAM:        2.0 MB RSS",
		"DB Size:    3.0 MB",
		"Alpha",
		"Beta",
		"Reason:     missing credentials",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output, got %q", want, got)
		}
	}
}

// Verifies buildInfoSnapshot captures running-daemon, initialized-data, indexed-search, and mixed source availability in one pass.
func TestBuildInfoSnapshot_runningDaemon(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	dataDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "test.plist")
	if err := os.WriteFile(plist, []byte("plist"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	BuildVersion = "build-version"
	BuildTime = "build-time"
	infoDataDir = func() string { return dataDir }
	infoPlistPath = func() string { return plist }
	infoStat = os.Stat
	infoIsNetworkAvailable = func() bool { return true }
	infoLaunchctlOutput = func(context.Context) ([]byte, error) { return []byte("123"), nil }
	infoDaemonRSSBytes = func(string) int64 { return 2 * 1024 * 1024 }
	infoSearchIndexStats = func(string) SearchIndexStats { return SearchIndexStats{Entries: 6} }
	infoFileGroupSizeBytes = func(string) int64 { return 4 * 1024 * 1024 }
	infoSourceDefs = []infoSourceDef{
		{
			Title: "Alpha",
			Key:   "alpha",
			InfoLines: func(string) []string {
				return []string{"   Status:     enabled"}
			},
		},
		{
			Title: "Beta",
			Key:   "beta",
			InfoLines: func(string) []string {
				t.Fatal("unavailable source should not render lines")
				return nil
			},
		},
	}
	infoSourceAvailability = func(key string) (bool, string) {
		if key == "beta" {
			return false, "missing credentials"
		}
		return true, ""
	}

	got := buildInfoSnapshot()
	if got.Build.Version != "build-version" || got.Build.Built != "build-time" {
		t.Fatalf("unexpected build snapshot: %#v", got.Build)
	}
	if !got.CoreDaemon.Installed || !got.CoreDaemon.Running || got.CoreDaemon.Status != "running" {
		t.Fatalf("unexpected daemon snapshot: %#v", got.CoreDaemon)
	}
	if !got.Data.Initialized || got.Data.Status != "initialized" {
		t.Fatalf("unexpected data snapshot: %#v", got.Data)
	}
	if got.SearchIndex.Entries != 6 || got.SearchIndex.DBSize == "" {
		t.Fatalf("unexpected search snapshot: %#v", got.SearchIndex)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("expected two sources, got %#v", got.Sources)
	}
	if got.Sources[0].Key != "alpha" || len(got.Sources[0].Lines) != 1 {
		t.Fatalf("expected available alpha source, got %#v", got.Sources[0])
	}
	if got.Sources[1].Key != "beta" || got.Sources[1].Available || got.Sources[1].Reason != "missing credentials" {
		t.Fatalf("expected unavailable beta source, got %#v", got.Sources[1])
	}
}

// Verifies buildInfoCoreDaemonSnapshot reports an installed but stopped daemon when launchctl returns no running instance.
func TestBuildInfoCoreDaemonSnapshot_installedNotRunning(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	dataDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "test.plist")
	if err := os.WriteFile(plist, []byte("plist"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	infoPlistPath = func() string { return plist }
	infoStat = os.Stat
	infoIsNetworkAvailable = func() bool { return false }
	infoLaunchctlOutput = func(context.Context) ([]byte, error) { return nil, errors.New("not loaded") }

	got := buildInfoCoreDaemonSnapshot(dataDir)
	if got.Network != "offline (sync paused)" {
		t.Fatalf("expected offline network status, got %#v", got)
	}
	if !got.Installed || got.Running || got.Status != "installed (not running)" {
		t.Fatalf("expected installed-not-running daemon, got %#v", got)
	}
	if got.Plist == "" || got.Logs == "" {
		t.Fatalf("expected plist and logs paths, got %#v", got)
	}
}

// Verifies buildInfoCoreDaemonSnapshot returns the not-installed state when no LaunchAgent plist exists.
func TestBuildInfoCoreDaemonSnapshot_notInstalled(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	dataDir := t.TempDir()
	infoPlistPath = func() string { return filepath.Join(t.TempDir(), "missing.plist") }
	infoStat = os.Stat
	infoIsNetworkAvailable = func() bool { return false }

	got := buildInfoCoreDaemonSnapshot(dataDir)
	if got.Installed || got.Running || got.Status != "not installed" {
		t.Fatalf("expected not-installed daemon snapshot, got %#v", got)
	}
}

// Verifies buildInfoDataSnapshot reports a missing data directory with the login hint used by the text report.
func TestBuildInfoDataSnapshot_notInitialized(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	infoStat = os.Stat
	got := buildInfoDataSnapshot(filepath.Join(t.TempDir(), "missing"))
	if got.Initialized {
		t.Fatalf("expected uninitialized data snapshot, got %#v", got)
	}
	if !strings.Contains(got.Status, "whatsapp login") {
		t.Fatalf("expected login hint in status, got %#v", got)
	}
}

// Verifies buildInfoSearchIndexSnapshot marks empty indexes as not indexed without adding DB metadata.
func TestBuildInfoSearchIndexSnapshot_notIndexed(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	infoSearchIndexStats = func(string) SearchIndexStats { return SearchIndexStats{} }
	infoFileGroupSizeBytes = func(string) int64 { return 0 }

	got := buildInfoSearchIndexSnapshot(t.TempDir(), true)
	if got.Status != "not indexed" || got.DBSize != "" {
		t.Fatalf("expected not-indexed snapshot, got %#v", got)
	}
}

// Verifies buildInfoSearchIndexSnapshot includes DB size and entry count when entries exist.
func TestBuildInfoSearchIndexSnapshot_indexed(t *testing.T) {
	restore := restoreStatusTestGlobals(t)
	defer restore()

	infoSearchIndexStats = func(string) SearchIndexStats { return SearchIndexStats{Entries: 5} }
	infoFileGroupSizeBytes = func(string) int64 { return 2 * 1024 * 1024 }

	got := buildInfoSearchIndexSnapshot(t.TempDir(), false)
	if got.Entries != 5 || got.DBSize == "" || got.Status != "" {
		t.Fatalf("expected indexed snapshot with DB size, got %#v", got)
	}
}

// Verifies writeSearchIndexSection renders entry count, DB size, and optional status.
func TestWriteSearchIndexSection_indexed(t *testing.T) {
	var b strings.Builder
	writeSearchIndexSection(&b, infoSearchIndexSnapshot{
		Entries: 2,
		DBSize:  "1.0 MB",
		Status:  "indexing in progress",
	})

	got := b.String()
	if !strings.Contains(got, "Entries:") {
		t.Fatalf("expected Entries label in output, got %q", got)
	}
	if !strings.Contains(got, "DB Size:") {
		t.Fatalf("expected DB Size label in output, got %q", got)
	}
	if !strings.Contains(got, "indexing in progress") {
		t.Fatalf("expected status in output, got %q", got)
	}
}

// Verifies writeSearchIndexSection falls back to the not-indexed message when the snapshot has zero counts.
func TestWriteSearchIndexSection_notIndexed(t *testing.T) {
	var b strings.Builder
	writeSearchIndexSection(&b, infoSearchIndexSnapshot{})

	got := b.String()
	if !strings.Contains(got, "Status:     not indexed") {
		t.Fatalf("expected not-indexed label in output, got %q", got)
	}
}
