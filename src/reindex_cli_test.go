package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

// Verifies handleReindex signals the running daemon instead of starting a local reindex.
func TestHandleReindex_signalsRunningDaemon(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldSignalProcess := reindexSignalProcess
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 123 }
	reindexLocalRunner = func([]string) error {
		t.Fatal("expected local reindex to be skipped when daemon is running")
		return nil
	}
	reindexSignalProcess = func(pid int, signal syscall.Signal) error {
		if pid != 123 || signal != syscall.SIGUSR1 {
			t.Fatalf("signal args = (%d, %v)", pid, signal)
		}
		return nil
	}
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexSignalProcess = oldSignalProcess
		reindexLocalRunner = oldLocalRunner
	}()

	stdout := captureMainStdout(t, func() {
		if err := handleReindex(nil); err != nil {
			t.Fatalf("handleReindex: %v", err)
		}
	})
	if !strings.Contains(stdout, "Signaled core daemon (PID 123) to reindex.") {
		t.Fatalf("unexpected stdout: %q", stdout)
	}
}

// Verifies handleReindex rejects --clear while the daemon is running so the
// shared search DB is not cleared underneath active indexing work.
func TestHandleReindex_clearRequiresStoppedDaemon(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 123 }
	reindexLocalRunner = func([]string) error {
		t.Fatal("expected local reindex to be skipped when daemon is running")
		return nil
	}
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexLocalRunner = oldLocalRunner
	}()

	err := handleReindex([]string{"--clear"})
	if err == nil || !strings.Contains(err.Error(), "--clear requires the core daemon to be stopped first") {
		t.Fatalf("handleReindex(--clear) error = %v", err)
	}
}

// Verifies handleReindex returns a useful error when signaling the running
// daemon fails instead of silently falling back to duplicate local work.
func TestHandleReindex_signalFailure(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldSignalProcess := reindexSignalProcess
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 123 }
	reindexLocalRunner = func([]string) error {
		t.Fatal("expected local reindex to be skipped after signal failure")
		return nil
	}
	reindexSignalProcess = func(int, syscall.Signal) error { return errors.New("kill failed") }
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexSignalProcess = oldSignalProcess
		reindexLocalRunner = oldLocalRunner
	}()

	err := handleReindex(nil)
	if err == nil || !strings.Contains(err.Error(), "signal core daemon reindex: kill failed") {
		t.Fatalf("handleReindex() error = %v", err)
	}
}

// Verifies handleReindex falls back to the standalone reindex path when no daemon is running.
func TestHandleReindex_runsLocalWhenDaemonStopped(t *testing.T) {
	oldDaemonPID := reindexDaemonPID
	oldLocalRunner := reindexLocalRunner
	reindexDaemonPID = func() int { return 0 }
	localCalled := false
	reindexLocalRunner = func(args []string) error {
		localCalled = true
		if len(args) != 1 || args[0] != "--clear" {
			t.Fatalf("local args = %v", args)
		}
		return nil
	}
	defer func() {
		reindexDaemonPID = oldDaemonPID
		reindexLocalRunner = oldLocalRunner
	}()

	if err := handleReindex([]string{"--clear"}); err != nil {
		t.Fatalf("handleReindex: %v", err)
	}
	if !localCalled {
		t.Fatal("expected local reindex runner to be called")
	}
}

// captureMainStdout captures stdout for one function call so CLI tests can assert on user-facing messages.
func captureMainStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}
