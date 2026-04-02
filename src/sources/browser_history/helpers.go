package browser_history

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const chromeEpochOffsetMicros int64 = 11644473600000000

var userHomeDir = os.UserHomeDir
var mkdirAll = os.MkdirAll
var openPath = os.Open
var createPath = os.Create
var copyData = io.Copy
var renamePath = os.Rename
var statPath = os.Stat
var removePath = os.Remove

// sourceFileMeta stores the source History file metadata used to decide whether a copy is needed.
type sourceFileMeta struct {
	Size       int64
	ModifiedAt time.Time
}

// Converts chrome microsecond timestamps (1601 epoch) into UTC time for tool responses and indexing.
func chromeMicrosToTime(chromeMicros int64) time.Time {
	unixMicros := chromeMicros - chromeEpochOffsetMicros
	return time.UnixMicro(unixMicros).UTC()
}

// Resolves the browser History SQLite path for the current user and OS.
func resolveHistoryPath(browser string) (string, error) {
	home, err := userHomeDir()
	if err != nil { // nocov
		return "", err
	}
	return resolveHistoryPathForOS(browser, home, runtime.GOOS)
}

// Resolves browser History SQLite path for a specific OS/home pair to keep path mapping testable.
func resolveHistoryPathForOS(browser, home, goos string) (string, error) {
	name := normalizeBrowser(browser)
	if name == "" {
		return "", fmt.Errorf("unsupported browser %q", browser)
	}
	if goos != "darwin" {
		return "", fmt.Errorf("browser_history currently supports macOS only")
	}
	switch name {
	case "chrome":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Default", "History"), nil
	case "brave":
		return filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser", "Default", "History"), nil
	default:
		// nocov
		return "", fmt.Errorf("unsupported browser %q", browser)
	}
}

// Normalizes browser input into canonical source values used in config and runtime logic.
func normalizeBrowser(browser string) string {
	switch strings.ToLower(strings.TrimSpace(browser)) {
	case "chrome":
		return "chrome"
	case "brave":
		return "brave"
	default:
		return ""
	}
}

// Copies browser History plus sidecar files into the source snapshot path for read-only querying.
func copyHistorySnapshot(sourcePath, destPath string) (sourceFileMeta, error) {
	if err := mkdirAll(filepath.Dir(destPath), 0755); err != nil { // nocov
		return sourceFileMeta{}, err
	}

	meta, err := copyFile(sourcePath, destPath)
	if err != nil {
		return sourceFileMeta{}, err
	}
	if err := copyOptionalSidecar(sourcePath+"-wal", destPath+"-wal"); err != nil {
		return sourceFileMeta{}, err
	}
	if err := copyOptionalSidecar(sourcePath+"-shm", destPath+"-shm"); err != nil {
		return sourceFileMeta{}, err
	}
	return meta, nil
}

// Copies one file atomically via a temp target so readers never observe partially written snapshots.
func copyFile(sourcePath, destPath string) (sourceFileMeta, error) {
	src, err := openPath(sourcePath)
	if err != nil {
		return sourceFileMeta{}, err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil { // nocov
		return sourceFileMeta{}, err
	}

	tmpPath := destPath + ".tmp"
	dst, err := createPath(tmpPath)
	if err != nil {
		return sourceFileMeta{}, err
	}
	if _, err := copyData(dst, src); err != nil {
		dst.Close()
		_ = removePath(tmpPath)
		return sourceFileMeta{}, err
	}
	if err := dst.Close(); err != nil { // nocov
		_ = removePath(tmpPath)
		return sourceFileMeta{}, err
	}
	if err := renamePath(tmpPath, destPath); err != nil {
		_ = removePath(tmpPath)
		return sourceFileMeta{}, err
	}
	return sourceFileMeta{Size: info.Size(), ModifiedAt: info.ModTime()}, nil
}

// Copies a SQLite sidecar file when present and removes stale destination sidecars when absent.
func copyOptionalSidecar(sourcePath, destPath string) error {
	if _, err := statPath(sourcePath); err != nil {
		if os.IsNotExist(err) {
			if rmErr := removePath(destPath); rmErr != nil && !os.IsNotExist(rmErr) {
				return rmErr
			}
			return nil
		}
		return err
	}
	_, err := copyFile(sourcePath, destPath)
	return err
}
