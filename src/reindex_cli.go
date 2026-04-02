package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"mcpyeahyouknowme/core"
)

var reindexDaemonPID = coreDaemonPID
var reindexSignalProcess = func(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}
var reindexLocalRunner = runLocalReindex

// runReindex routes manual reindex requests to the daemon when it is running,
// or falls back to a standalone local rebuild when no daemon exists.
func runReindex(args []string) {
	if err := handleReindex(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// handleReindex routes reindex requests so one process owns indexing work and
// `--clear` remains a stop-the-daemon-first maintenance path.
func handleReindex(args []string) error {
	clearIndex := len(args) > 0 && args[0] == "--clear"
	if pid := reindexDaemonPID(); pid > 0 {
		if clearIndex {
			return errors.New("--clear requires the core daemon to be stopped first")
		}
		if err := reindexSignalProcess(pid, syscall.SIGUSR1); err != nil {
			return fmt.Errorf("signal core daemon reindex: %w", err)
		}
		fmt.Printf("Signaled core daemon (PID %d) to reindex.\n", pid)
		return nil
	}
	return reindexLocalRunner(args)
}

// runLocalReindex performs a full standalone search index rebuild with progress
// output for cases where the daemon is not installed or not currently running.
func runLocalReindex(args []string) error {
	dir := core.DataDir()

	clearIndex := len(args) > 0 && args[0] == "--clear"

	embedder, err := NewEmbedder(filepath.Join(dir, "models"))
	if err != nil {
		return err
	}
	defer embedder.Close()

	searchStore, err := NewSearchStore(dir, embedder)
	if err != nil {
		return fmt.Errorf("search index unavailable: %w", err)
	}
	defer searchStore.Close()

	if clearIndex {
		fmt.Fprintln(os.Stderr, "Clearing existing index...")
		searchStore.db.Exec("DELETE FROM search_embeddings")
		searchStore.db.Exec("DELETE FROM search_entries")
		searchStore.db.Exec("INSERT INTO search_fts(search_fts) VALUES('rebuild')")
		searchStore.db.Exec("DELETE FROM search_meta")
	}

	sources := buildActiveSources(dir)
	defer func() {
		for _, s := range sources {
			s.src.Close()
		}
	}()

	fmt.Fprintf(os.Stderr, "Indexing %d source(s)...\n", len(sources))
	for _, active := range sources {
		if !active.desc.IndexGlobally {
			continue
		}
		fmt.Fprintf(os.Stderr, "  %s: loading entries...\n", active.src.Name())
		entries, err := active.src.SearchEntries()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: error: %v\n", active.src.Name(), err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %s: indexing %d entries...\n", active.src.Name(), len(entries))
		if err := searchStore.IndexEntries(entries); err != nil {
			fmt.Fprintf(os.Stderr, "  %s: index error: %v\n", active.src.Name(), err)
			continue
		}
		searchStore.UpdateSourceTimestamp(active.src.Name(), time.Now())
		fmt.Fprintf(os.Stderr, "  %s: done\n", active.src.Name())
	}

	fmt.Fprintln(os.Stderr, "  embeddings: computing pending vectors...")
	if err := searchStore.ComputePendingEmbeddings(); err != nil {
		fmt.Fprintf(os.Stderr, "  embeddings: error: %v\n", err)
	}

	stats := searchStore.IndexStats()
	fmt.Fprintf(os.Stderr, "\nComplete: %d entries, %d embedded (%d%%)\n",
		stats.Entries, stats.Embedded, stats.Embedded*100/max(stats.Entries, 1))
	return nil
}
