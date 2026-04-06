package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"mcpyeahyouknowme/core"
)

var reindexDaemonPID = coreDaemonPID
var reindexSignalProcess = func(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}
var reindexDataDir = core.DataDir
var reindexNewSearchStore = func(dir string) (*SearchStore, error) {
	return NewSearchStore(dir)
}
var reindexActiveSources = buildActiveSources
var reindexLocalRunner = runLocalReindex

// runReindex routes manual reindex requests to the daemon when it is running,
// or falls back to a standalone local rebuild when no daemon exists.
func runReindex(args []string) {
	if err := handleReindex(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Routes reindex requests so one process owns indexing work and every manual run does a full clear-and-rebuild.
func handleReindex(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("reindex does not accept arguments")
	}
	if pid := reindexDaemonPID(); pid > 0 {
		if err := reindexSignalProcess(pid, syscall.SIGUSR1); err != nil {
			return fmt.Errorf("signal core daemon reindex: %w", err)
		}
		fmt.Printf("Signaled core daemon (PID %d) to reindex.\n", pid)
		return nil
	}
	return reindexLocalRunner(args)
}

// Performs a full standalone search index rebuild with progress output when no daemon is running.
func runLocalReindex(_ []string) error {
	dir := reindexDataDir()

	searchStore, err := reindexNewSearchStore(dir)
	if err != nil {
		return fmt.Errorf("search index unavailable: %w", err)
	}
	defer searchStore.Close()

	fmt.Fprintln(os.Stderr, "Clearing existing index...")
	if err := searchStore.Clear(); err != nil {
		return fmt.Errorf("clear search index: %w", err)
	}

	sources := reindexActiveSources(dir)
	defer func() {
		for _, s := range sources {
			s.src.Close()
		}
	}()

	fmt.Fprintln(os.Stderr, "Preparing bulk search writes...")
	if err := searchStore.BeginBulkIndex(); err != nil {
		return fmt.Errorf("enable bulk FTS indexing: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Indexing %d source(s)...\n", len(sources))
	for _, active := range sources {
		if !active.desc.IndexGlobally {
			continue
		}
		fmt.Fprintf(os.Stderr, "  %s: loading entries...\n", active.src.Name())
		count := 0
		emit := func(batch []core.SearchEntry) error {
			count += len(batch)
			return searchStore.IndexEntries(batch)
		}
		if streaming, ok := active.src.(core.StreamingSource); ok {
			if err := streaming.StreamSearchEntries(emit); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: error: %v\n", active.src.Name(), err)
				continue
			}
		} else {
			entries, err := active.src.SearchEntries()
			if err != nil {
				fmt.Fprintf(os.Stderr, "  %s: error: %v\n", active.src.Name(), err)
				continue
			}
			if err := emit(entries); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: index error: %v\n", active.src.Name(), err)
				continue
			}
		}
		fmt.Fprintf(os.Stderr, "  %s: indexed %d entries...\n", active.src.Name(), count)
		searchStore.UpdateSourceTimestamp(active.src.Name(), time.Now())
		fmt.Fprintf(os.Stderr, "  %s: done\n", active.src.Name())
	}

	fmt.Fprintln(os.Stderr, "Finalizing FTS rebuild...")
	if err := searchStore.EndBulkIndex(); err != nil {
		return fmt.Errorf("finalize bulk FTS indexing: %w", err)
	}

	stats := searchStore.IndexStats()
	fmt.Fprintf(os.Stderr, "\nComplete: %d entries indexed\n", stats.Entries)
	return nil
}
