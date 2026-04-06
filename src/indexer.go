package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

type activeSource struct {
	desc   registry.Descriptor
	src    core.DataSource
	config core.SourceConfig
}

type sourceIndexer interface {
	IndexEntries(entries []core.SearchEntry) error
	PruneSourceKeys(source string, current []indexKey) error
	UpdateSourceTimestamp(source string, ts time.Time)
	LastIndexed(source string) time.Time
}

// Populates the search index from all eligible global sources until the
// context is canceled, using full passes for prune-capable rebuilds and
// incremental passes when sources can prove they are unchanged.
func indexSources(ctx context.Context, store sourceIndexer, sources []activeSource, fullPass bool) bool {
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
		if !shouldIndexSource(fullPass, store, active) {
			continue
		}
		keys, completed, err := indexSourceEntries(ctx, store, active)
		if !completed {
			return false
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get search entries from %s: %v\n", active.src.Name(), err)
			continue
		}
		if fullPass {
			if err := store.PruneSourceKeys(active.src.Name(), keys); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to prune %s entries: %v\n", active.src.Name(), err)
				continue
			}
		}
		store.UpdateSourceTimestamp(active.src.Name(), time.Now())
	}
	return true
}

// Checks whether an incremental pass can skip a source based on its stored
// watermark and optional IncrementalSource change detection.
func shouldIndexSource(fullPass bool, store sourceIndexer, active activeSource) bool {
	if fullPass {
		return true
	}
	incremental, ok := active.src.(core.IncrementalSource)
	if !ok {
		return true
	}
	return incremental.HasChangesSince(store.LastIndexed(active.src.Name()))
}

// Streams or loads one source's search entries into the store while collecting
// lightweight prune keys for full-pass cleanup.
func indexSourceEntries(ctx context.Context, store sourceIndexer, active activeSource) ([]indexKey, bool, error) {
	var keys []indexKey
	emit := func(batch []core.SearchEntry) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(batch) == 0 {
			return nil
		}
		if err := store.IndexEntries(batch); err != nil {
			return err
		}
		keys = append(keys, collectIndexKeys(batch)...)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}

	if streaming, ok := active.src.(core.StreamingSource); ok {
		if err := streaming.StreamSearchEntries(emit); err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil, false, nil
			}
			return nil, true, err
		}
		return keys, true, nil
	}

	entries, err := active.src.SearchEntries()
	if err != nil {
		return nil, true, err
	}
	if err := emit(entries); err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil, false, nil
		}
		return nil, true, err
	}
	return keys, true, nil
}

// Collects the source_id/content_type pairs needed by prune passes from one
// batch of search entries without retaining titles, bodies, or metadata.
func collectIndexKeys(entries []core.SearchEntry) []indexKey {
	keys := make([]indexKey, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, indexKey{
			SourceID:    entry.SourceID,
			ContentType: entry.ContentType,
		})
	}
	return keys
}

// buildActiveSources constructs DataSource instances for all available, enabled,
// authenticated sources from the registry.
func buildActiveSources(dir string) []activeSource {
	return reconcileIndexSources(dir, nil)
}

// Reconciles the active indexing source pool against current config and auth so
// unchanged sources can be reused across daemon index passes.
func reconcileIndexSources(dir string, existing []activeSource) []activeSource {
	cfg := core.LoadConfig(dir)
	existingByName := make(map[string]activeSource, len(existing))
	for _, active := range existing {
		existingByName[active.desc.Name] = active
	}

	var sources []activeSource
	for _, desc := range registry.All {
		available, _ := registry.IsAvailable(desc.Name)
		if !available {
			closeActiveSource(existingByName, desc.Name)
			continue
		}
		sc := cfg.Sources[desc.Name]
		enabled := sc.Enabled || (!desc.RunsCore && !desc.IndexGlobally)
		if !enabled {
			closeActiveSource(existingByName, desc.Name)
			continue
		}
		if desc.IsAuthenticated != nil && !desc.IsAuthenticated(dir) {
			closeActiveSource(existingByName, desc.Name)
			continue
		}

		if current, ok := existingByName[desc.Name]; ok && sourceConfigEqual(current.config, sc) {
			sources = append(sources, current)
			delete(existingByName, desc.Name)
			continue
		}
		closeActiveSource(existingByName, desc.Name)
		src := desc.New(dir)
		if src == nil {
			continue
		}
		sources = append(sources, activeSource{
			desc:   desc,
			src:    src,
			config: cloneSourceConfig(sc),
		})
	}

	for name := range existingByName {
		closeActiveSource(existingByName, name)
	}
	return sources
}

// Closes and removes one active source from a reuse map when config or auth
// changes mean the old instance can no longer be trusted for future passes.
func closeActiveSource(existing map[string]activeSource, name string) {
	active, ok := existing[name]
	if !ok {
		return
	}
	delete(existing, name)
	if active.src != nil {
		active.src.Close()
	}
}

// Copies one source config so reused-source comparisons are insulated from
// future config mutations and shared json.RawMessage backing arrays.
func cloneSourceConfig(sc core.SourceConfig) core.SourceConfig {
	cloned := sc
	if len(sc.Auth) > 0 {
		cloned.Auth = append([]byte(nil), sc.Auth...)
	}
	return cloned
}

// Compares the persisted fields that affect indexing behavior so the daemon
// rebuilds source objects only when their config inputs actually changed.
func sourceConfigEqual(a, b core.SourceConfig) bool {
	return a.Enabled == b.Enabled &&
		a.Reset == b.Reset &&
		bytes.Equal(a.Auth, b.Auth)
}
