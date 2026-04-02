package browser_history

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verifies Chrome microsecond timestamps convert to the expected UTC instant.
func TestChromeMicrosToTime(t *testing.T) {
	got := chromeMicrosToTime(chromeEpochOffsetMicros)
	if !got.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("chromeMicrosToTime() = %v, want unix epoch", got)
	}
}

// Verifies browser path resolution maps chrome and brave on macOS and rejects unsupported inputs.
func TestResolveHistoryPathForOS(t *testing.T) {
	home := "/Users/tester"
	chromePath, err := resolveHistoryPathForOS("chrome", home, "darwin")
	if err != nil {
		t.Fatalf("resolve chrome: %v", err)
	}
	wantChrome := filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Default", "History")
	if chromePath != wantChrome {
		t.Fatalf("chrome path = %q, want %q", chromePath, wantChrome)
	}

	bravePath, err := resolveHistoryPathForOS("brave", home, "darwin")
	if err != nil {
		t.Fatalf("resolve brave: %v", err)
	}
	wantBrave := filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser", "Default", "History")
	if bravePath != wantBrave {
		t.Fatalf("brave path = %q, want %q", bravePath, wantBrave)
	}

	if _, err := resolveHistoryPathForOS("edge", home, "darwin"); err == nil {
		t.Fatal("expected unsupported browser error")
	}
	if _, err := resolveHistoryPathForOS("chrome", home, "linux"); err == nil {
		t.Fatal("expected unsupported os error")
	}
}

// Verifies current-OS path resolution succeeds for supported browsers on macOS.
func TestResolveHistoryPath(t *testing.T) {
	path, err := resolveHistoryPath("chrome")
	if err != nil {
		t.Fatalf("resolveHistoryPath chrome: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
}

// Verifies current-OS path resolution returns the home-dir error when the environment lookup fails.
func TestResolveHistoryPath_homeError(t *testing.T) {
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) { return "", assertErr("home failed") }
	defer func() { userHomeDir = oldUserHomeDir }()

	if _, err := resolveHistoryPath("chrome"); err == nil {
		t.Fatal("expected home directory error")
	}
}

// Verifies browser name normalization handles case and surrounding whitespace.
func TestNormalizeBrowser(t *testing.T) {
	if got := normalizeBrowser("  ChRoMe "); got != "chrome" {
		t.Fatalf("normalize chrome = %q", got)
	}
	if got := normalizeBrowser("brave"); got != "brave" {
		t.Fatalf("normalize brave = %q", got)
	}
	if got := normalizeBrowser("firefox"); got != "" {
		t.Fatalf("normalize firefox = %q, want empty", got)
	}
}

// Verifies snapshot copy writes primary and sidecar files and removes stale sidecars.
func TestCopyHistorySnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "History")
	dst := filepath.Join(dir, "copy", "browser_history.db")
	if err := os.WriteFile(src, []byte("main"), 0644); err != nil {
		t.Fatalf("write src main: %v", err)
	}
	if err := os.WriteFile(src+"-wal", []byte("wal"), 0644); err != nil {
		t.Fatalf("write src wal: %v", err)
	}

	meta, err := copyHistorySnapshot(src, dst)
	if err != nil {
		t.Fatalf("copyHistorySnapshot: %v", err)
	}
	if meta.Size == 0 {
		t.Fatal("expected non-zero size metadata")
	}
	if _, err := os.Stat(dst + "-wal"); err != nil {
		t.Fatalf("expected copied wal file: %v", err)
	}

	if err := os.Remove(src + "-wal"); err != nil {
		t.Fatalf("remove source wal: %v", err)
	}
	if err := copyOptionalSidecar(src+"-wal", dst+"-wal"); err != nil {
		t.Fatalf("copyOptionalSidecar remove stale: %v", err)
	}
	if _, err := os.Stat(dst + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("expected stale wal removed, stat err=%v", err)
	}
}

// Verifies snapshot copy surfaces a primary-file copy error before attempting sidecars.
func TestCopyHistorySnapshot_mainCopyError(t *testing.T) {
	if _, err := copyHistorySnapshot(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "copy.db")); err == nil {
		t.Fatal("expected primary copy error")
	}
}

// Verifies snapshot copy returns sidecar failures from either WAL or SHM copies instead of silently succeeding.
func TestCopyHistorySnapshot_sidecarErrors(t *testing.T) {
	tests := []struct {
		name          string
		failingSuffix string
	}{
		{name: "wal failure", failingSuffix: "browser_history.db-wal"},
		{name: "shm failure", failingSuffix: "browser_history.db-shm"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "History")
			dst := filepath.Join(dir, "copy", "browser_history.db")
			if err := os.WriteFile(src, []byte("main"), 0o644); err != nil {
				t.Fatalf("write src main: %v", err)
			}
			if err := os.WriteFile(src+"-wal", []byte("wal"), 0o644); err != nil {
				t.Fatalf("write src wal: %v", err)
			}
			if err := os.WriteFile(src+"-shm", []byte("shm"), 0o644); err != nil {
				t.Fatalf("write src shm: %v", err)
			}

			oldRenamePath := renamePath
			renamePath = func(oldPath, newPath string) error {
				if strings.HasSuffix(newPath, tc.failingSuffix) {
					return assertErr("rename failed")
				}
				return oldRenamePath(oldPath, newPath)
			}
			defer func() { renamePath = oldRenamePath }()

			if _, err := copyHistorySnapshot(src, dst); err == nil {
				t.Fatal("expected sidecar copy error")
			}
		})
	}
}

// Verifies copyFile surfaces open, create, copy, and rename failures while cleaning up temp files.
func TestCopyFile_errorPaths(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, src, dst string)
	}{
		{
			name: "missing source",
			run: func(t *testing.T, src, dst string) {
				if _, err := copyFile(src, dst); err == nil {
					t.Fatal("expected open error")
				}
			},
		},
		{
			name: "create failure",
			run: func(t *testing.T, src, dst string) {
				if err := os.WriteFile(src, []byte("main"), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				oldCreatePath := createPath
				createPath = func(string) (*os.File, error) { return nil, assertErr("create failed") }
				defer func() { createPath = oldCreatePath }()

				if _, err := copyFile(src, dst); err == nil {
					t.Fatal("expected create error")
				}
			},
		},
		{
			name: "copy failure",
			run: func(t *testing.T, src, dst string) {
				if err := os.Mkdir(src, 0o755); err != nil {
					t.Fatalf("mkdir src: %v", err)
				}
				if _, err := copyFile(src, dst); err == nil {
					t.Fatal("expected copy error")
				}
			},
		},
		{
			name: "rename failure",
			run: func(t *testing.T, src, dst string) {
				if err := os.WriteFile(src, []byte("main"), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				oldRenamePath := renamePath
				renamePath = func(string, string) error { return assertErr("rename failed") }
				defer func() { renamePath = oldRenamePath }()

				if _, err := copyFile(src, dst); err == nil {
					t.Fatal("expected rename error")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "History")
			dst := filepath.Join(dir, "copy.db")
			tc.run(t, src, dst)
		})
	}
}

// Verifies copyOptionalSidecar surfaces non-NotExist stat errors instead of treating them like stale sidecars.
func TestCopyOptionalSidecar_statError(t *testing.T) {
	oldStatPath := statPath
	statPath = func(string) (os.FileInfo, error) { return nil, errors.New("stat failed") }
	defer func() { statPath = oldStatPath }()

	if err := copyOptionalSidecar("source", "dest"); err == nil {
		t.Fatal("expected stat error")
	}
}

// Verifies copyOptionalSidecar surfaces stale-destination cleanup failures when the source sidecar is absent.
func TestCopyOptionalSidecar_removeError(t *testing.T) {
	oldRemovePath := removePath
	removePath = func(string) error { return errors.New("remove failed") }
	defer func() { removePath = oldRemovePath }()

	if err := copyOptionalSidecar(filepath.Join(t.TempDir(), "missing"), "dest"); err == nil {
		t.Fatal("expected remove error")
	}
}
