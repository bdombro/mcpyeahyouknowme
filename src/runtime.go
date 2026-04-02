package main

import (
	"bytes"
	"context"
	"errors"
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

// indexCoordinator serializes background index runs and can request a restart after the current run yields.
type indexCoordinator struct {
	mu             sync.Mutex
	running        bool
	restartPending bool
	clearPending   bool
	cancel         context.CancelFunc
	start          func(context.Context, bool)
}

// Builds a coordinator that owns one cancellable background indexing worker at a time.
func newIndexCoordinator(start func(context.Context, bool)) *indexCoordinator {
	return &indexCoordinator{start: start}
}

// Starts a new index run or requests cancellation-and-restart when one is already active.
func (c *indexCoordinator) Request(restartIfRunning, clearFirst bool) {
	if c == nil || c.start == nil {
		return
	}

	c.mu.Lock()
	if c.running {
		if restartIfRunning {
			c.restartPending = true
			c.clearPending = c.clearPending || clearFirst
			if c.cancel != nil {
				c.cancel()
			}
		}
		c.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.running = true
	c.restartPending = false
	c.clearPending = false
	c.cancel = cancel
	c.mu.Unlock()

	go func() {
		c.start(ctx, clearFirst)

		c.mu.Lock()
		c.running = false
		c.cancel = nil
		restart := c.restartPending
		clearFirstNext := c.clearPending
		c.restartPending = false
		c.clearPending = false
		c.mu.Unlock()

		if restart {
			c.Request(false, clearFirstNext)
		}
	}()
}

// Cancels the active run and clears any queued restart so daemon shutdown does not relaunch indexing.
func (c *indexCoordinator) Stop() {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.restartPending = false
	c.clearPending = false
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

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

	coordinator := newIndexCoordinator(func(ctx context.Context, clearFirst bool) {
		if searchStore == nil {
			return
		}
		if clearFirst {
			if err := searchStore.Clear(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to clear search index: %v\n", err)
				return
			}
		}
		sources := buildActiveSources(dir)
		defer func() {
			for _, s := range sources {
				s.src.Close()
			}
		}()

		completed := indexSources(ctx, searchStore, sources)
		if !completed {
			return
		}
		if err := searchStore.ComputePendingEmbeddingsContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "Warning: embedding pass failed: %v\n", err)
		}
	})
	requestIndex := func(restartIfRunning, clearFirst bool) {
		if searchStore == nil {
			return
		}
		coordinator.Request(restartIfRunning, clearFirst)
	}

	requestIndex(false, false)

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)

	for {
		select {
		case sig := <-sigCh:
			if handleCoreSignal(sig, running, searchStore, embedder, coordinator.Stop, func() { requestIndex(true, true) }) {
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

			requestIndex(false, false)
		}
	}
}

// handleCoreSignal runs an immediate index pass for SIGUSR1 and otherwise performs daemon shutdown cleanup.
func handleCoreSignal(sig os.Signal, running map[string]context.CancelFunc, searchStore *SearchStore, embedder *Embedder, stopIndex func(), runIndex func()) bool {
	if sig == syscall.SIGUSR1 {
		runIndex()
		return false
	}
	if stopIndex != nil {
		stopIndex()
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
