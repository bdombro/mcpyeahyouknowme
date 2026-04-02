package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

// runCore is the long-lived daemon loop: it polls config, starts/stops/reset sources, and kicks optional search indexing on each tick.
func runCore() {
	dir := core.DataDir()
	cfg := loadConfig(dir)

	fmt.Println("Starting mcpyeahyouknowme core daemon...")
	fmt.Print(renderInfo())

	running := map[string]context.CancelFunc{}

	for name, sc := range cfg.Sources {
		if sc.Reset {
			handleReset(dir, name, &cfg)
			continue
		}
		if sc.Enabled {
			startSource(dir, name, running)
		}
	}

	embedder, err := NewEmbedder(filepath.Join(dir, "models"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: embedding init failed: %v (search indexing disabled)\n", err)
	}
	var searchStore *SearchStore
	if embedder != nil {
		searchStore, err = NewSearchStore(dir, embedder)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: search index unavailable: %v\n", err)
		}
	}

	var indexMu sync.Mutex
	runIndex := func() {
		if searchStore == nil {
			return
		}
		if !indexMu.TryLock() {
			return
		}
		go func() {
			defer indexMu.Unlock()
			sources := buildActiveSources(dir)
			defer func() {
				for _, s := range sources {
					s.src.Close()
				}
			}()
			indexSources(searchStore, sources)
		}()
	}

	runIndex()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)

	for {
		select {
		case sig := <-sigCh:
			if handleCoreSignal(sig, running, searchStore, embedder, runIndex) {
				return
			}
		case <-ticker.C:
			newCfg := loadConfig(dir)
			for name, sc := range newCfg.Sources {
				if sc.Reset {
					if cancel, ok := running[name]; ok {
						cancel()
						delete(running, name)
					}
					handleReset(dir, name, &newCfg)
				}
			}
			for name, cancel := range running {
				newSc, exists := newCfg.Sources[name]
				if !exists || !newSc.Enabled || newSc.Reset {
					continue
				}
				if shouldRestartSource(cfg.Sources[name], newSc) {
					cancel()
					delete(running, name)
				}
			}
			for name, sc := range newCfg.Sources {
				if sc.Enabled && !sc.Reset && running[name] == nil {
					startSource(dir, name, running)
				}
			}
			for name, cancel := range running {
				sc, exists := newCfg.Sources[name]
				if !exists || !sc.Enabled {
					cancel()
					delete(running, name)
				}
			}
			cfg = newCfg

			runIndex()
		}
	}
}

// handleCoreSignal runs an immediate index pass for SIGUSR1 and otherwise performs daemon shutdown cleanup.
func handleCoreSignal(sig os.Signal, running map[string]context.CancelFunc, searchStore *SearchStore, embedder *Embedder, runIndex func()) bool {
	if sig == syscall.SIGUSR1 {
		runIndex()
		return false
	}
	for _, cancel := range running {
		cancel()
	}
	if searchStore != nil {
		searchStore.Close()
	}
	if embedder != nil {
		embedder.Close()
	}
	return true
}

// shouldRestartSource reports whether auth changed while enable/reset state stayed stable, so core should rebuild the source.
func shouldRestartSource(prev, next core.SourceConfig) bool {
	return prev.Enabled == next.Enabled &&
		prev.Reset == next.Reset &&
		!bytes.Equal(prev.Auth, next.Auth)
}

// startSource constructs the source, checks auth, and starts its CoreService.
func startSource(dir, name string, running map[string]context.CancelFunc) {
	desc, ok := registry.Find(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "Warning: unknown source %q\n", name)
		return
	}
	if available, reason := registry.IsAvailable(name); !available {
		fmt.Fprintf(os.Stderr, "Info: %s is unavailable and will not be started.\n", name)
		if reason != "" {
			fmt.Fprintf(os.Stderr, "      %s.\n", reason)
		}
		return
	}
	if !desc.RunsCore {
		fmt.Printf("ℹ %s is enabled for MCP use but does not run a background core service\n", name)
		return
	}
	src := desc.New(dir)
	if src == nil {
		fmt.Fprintf(os.Stderr, "Warning: could not construct source %q\n", name)
		return
	}
	cs, ok := src.(core.CoreService)
	if !ok {
		fmt.Fprintf(os.Stderr, "Warning: source %q is marked RunsCore but does not implement core.CoreService\n", name)
		return
	}
	if cs.RequiresAuth() && !registry.IsAuthenticated(name, dir) {
		fmt.Printf("ℹ %s requires authentication - run 'mcpyeahyouknowme %s login' first\n",
			src.Description(), src.Name())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	running[name] = cancel
	go func() {
		if err := cs.StartCore(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Core service %s error: %v\n", name, err)
		}
		delete(running, name)
	}()
}

// handleReset calls source.Reset(), persists the source disabled, and zeroes cfg state so this poll tick stops treating it as active.
func handleReset(dir, name string, cfg *core.Config) {
	src := registry.NewSource(name, dir)
	if src != nil {
		if err := src.Reset(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Reset error for %s: %v\n", name, err)
		}
	}
	if err := core.SetSourceDisabled(dir, name); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not disable %s after reset: %v\n", name, err)
	}
	cfg.Sources[name] = core.SourceConfig{}
}
