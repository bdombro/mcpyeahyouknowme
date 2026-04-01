package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mcpyeahyouknowme/core"
)

// runReindex performs a full search index rebuild with progress output.
func runReindex(args []string) {
	dir := core.DataDir()

	clear := len(args) > 0 && args[0] == "--clear"

	embedder, err := NewEmbedder(filepath.Join(dir, "models"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer embedder.Close()

	searchStore, err := NewSearchStore(dir, embedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: search index unavailable: %v\n", err)
		os.Exit(1)
	}
	defer searchStore.Close()

	if clear {
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

	stats := searchStore.IndexStats()
	fmt.Fprintf(os.Stderr, "\nComplete: %d entries, %d embedded (%d%%)\n",
		stats.Entries, stats.Embedded, stats.Embedded*100/max(stats.Entries, 1))
}
