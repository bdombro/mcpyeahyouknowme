package core

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// IsNetworkAvailable reports whether any non-loopback network interface is up
// and has a routable (non-link-local) IP address. This is a pure OS syscall —
// zero network traffic, sub-millisecond — so it can be called freely.
func IsNetworkAvailable() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				return true
			}
		}
	}
	return false
}

// DefaultReset removes a list of files (by relative path under dataDir).
// Missing files are silently skipped. Returns the first non-missing error.
func DefaultReset(dataDir string, files []string) error {
	var firstErr error
	for _, f := range files {
		path := filepath.Join(dataDir, f)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", path, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// RunPollLoop runs fn immediately then on every interval tick until ctx is done.
// Errors from fn are printed but do not stop the loop.
// When the network is unavailable, the tick is skipped.
func RunPollLoop(ctx context.Context, interval time.Duration, fn func(context.Context) error) error {
	if IsNetworkAvailable() {
		if err := fn(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Sync error: %v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Network unavailable, skipping initial sync\n")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !IsNetworkAvailable() {
				fmt.Fprintf(os.Stderr, "Network unavailable, skipping sync\n")
				continue
			}
			if err := fn(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Sync error: %v\n", err)
			}
		}
	}
}

// OpenDB opens a SQLite database at filepath.Join(dataDir, filename) with
// shared pragmas (WAL mode, 30-second busy timeout, foreign keys on).
func OpenDB(dataDir, filename string) (*sql.DB, error) {
	path := filepath.Join(dataDir, filename)
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=30000", path))
	if err != nil {
		return nil, err
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	return db, nil
}
