package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

type activeSource struct {
	desc registry.Descriptor
	src  core.DataSource
}

type sourceIndexer interface {
	IndexEntries(entries []core.SearchEntry) error
	PruneSource(source string, current []core.SearchEntry) error
	UpdateSourceTimestamp(source string, ts time.Time)
}

// Populates the search index from all global sources until the context is canceled.
func indexSources(ctx context.Context, store sourceIndexer, sources []activeSource) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, active := range sources {
		if ctx.Err() != nil {
			return false
		}
		if !active.desc.IndexGlobally {
			continue
		}
		entries, err := active.src.SearchEntries()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get search entries from %s: %v\n", active.src.Name(), err)
			continue
		}
		if ctx.Err() != nil {
			return false
		}
		if err := store.IndexEntries(entries); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to index %s entries: %v\n", active.src.Name(), err)
			continue
		}
		if err := store.PruneSource(active.src.Name(), entries); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to prune %s entries: %v\n", active.src.Name(), err)
			continue
		}
		store.UpdateSourceTimestamp(active.src.Name(), time.Now())
	}
	return true
}

// buildActiveSources constructs DataSource instances for all available, enabled,
// authenticated sources from the registry.
func buildActiveSources(dir string) []activeSource {
	cfg := core.LoadConfig(dir)
	var sources []activeSource
	for _, desc := range registry.All {
		available, _ := registry.IsAvailable(desc.Name)
		if !available {
			continue
		}
		sc := cfg.Sources[desc.Name]
		enabled := sc.Enabled || (!desc.RunsCore && !desc.IndexGlobally)
		if !enabled {
			continue
		}
		if desc.IsAuthenticated != nil && !desc.IsAuthenticated(dir) {
			continue
		}
		sources = append(sources, activeSource{desc: desc, src: desc.New(dir)})
	}
	return sources
}
