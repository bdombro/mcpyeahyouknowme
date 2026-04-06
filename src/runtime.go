package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

const (
	coreLogTrimThresholdBytes = 5 * 1024 * 1024
	coreLogKeepTailBytes      = 1 * 1024 * 1024
)

var trimLogWrite = func(file *os.File, tail []byte) error {
	_, err := file.Write(tail)
	return err
}

// indexCoordinator serializes background index runs and can request a restart after the current run yields.
type indexCoordinator struct {
	mu              sync.Mutex
	running         bool
	restartPending  bool
	clearPending    bool
	fullPassPending bool
	cancel          context.CancelFunc
	start           func(context.Context, bool, bool)
	wg              sync.WaitGroup
}

// Builds a coordinator that owns one cancellable background indexing worker at a time.
func newIndexCoordinator(start func(context.Context, bool, bool)) *indexCoordinator {
	return &indexCoordinator{start: start}
}

// Starts a new index run or requests cancellation-and-restart when one is already active.
func (c *indexCoordinator) Request(restartIfRunning, clearFirst, fullPass bool) {
	if c == nil || c.start == nil {
		return
	}
	if clearFirst {
		fullPass = true
	}

	c.mu.Lock()
	if c.running {
		if restartIfRunning {
			c.restartPending = true
			c.clearPending = c.clearPending || clearFirst
			c.fullPassPending = c.fullPassPending || fullPass
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
	c.fullPassPending = false
	c.cancel = cancel
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		c.start(ctx, clearFirst, fullPass)

		c.mu.Lock()
		c.running = false
		c.cancel = nil
		restart := c.restartPending
		clearFirstNext := c.clearPending
		fullPassNext := c.fullPassPending
		c.restartPending = false
		c.clearPending = false
		c.fullPassPending = false
		c.mu.Unlock()

		if restart {
			c.Request(false, clearFirstNext, fullPassNext)
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
	c.fullPassPending = false
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
}

// Trims the daemon log file when it grows past the threshold so a long-lived LaunchAgent keeps recent context without unbounded growth.
func trimLogFile(dataDir string) {
	path := filepath.Join(dataDir, "core.log")
	if err := trimLogFilePath(path, coreLogTrimThresholdBytes, coreLogKeepTailBytes); err != nil {
		slog.Warn("could not trim log file", "path", path, "err", err)
	}
}

// Rewrites a large log file in place with only its newest tail so launchd-owned stdout/stderr file descriptors keep appending to the same inode.
func trimLogFilePath(path string, trimThresholdBytes, keepTailBytes int64) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() <= trimThresholdBytes || keepTailBytes <= 0 {
		return nil
	}

	start := info.Size() - keepTailBytes
	if start < 0 {
		start = 0
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	tail := make([]byte, info.Size()-start)
	n, err := file.ReadAt(tail, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	tail = tail[:n]
	if start > 0 {
		if newlineIndex := bytes.IndexByte(tail, '\n'); newlineIndex >= 0 {
			tail = tail[newlineIndex+1:]
		}
	}

	file, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer file.Close()

	if err := trimLogWrite(file, tail); err != nil {
		return err
	}
	return nil
}

// runCore is the long-lived daemon loop: it polls config, starts/stops/reset sources, and kicks optional search indexing on each tick.
func runCore() {
	dir := core.DataDir()
	trimLogFile(dir)
	cfg := loadConfig(dir)

	slog.Info("starting mcpyeahyouknowme core daemon", "data_dir", dir)

	running := map[string]context.CancelFunc{}
	searchStore, err := NewSearchStore(dir)
	if err != nil {
		slog.Warn("search index unavailable", "err", err)
	}

	for name, sc := range cfg.Sources {
		if sc.Reset {
			handleReset(dir, name, &cfg, searchStore)
			continue
		}
		if sc.Enabled {
			startSource(dir, name, running)
		}
	}

	indexSourcesPool := []activeSource{}
	closeIndexSources := func() {
		for _, active := range indexSourcesPool {
			if active.src != nil {
				active.src.Close()
			}
		}
		indexSourcesPool = nil
	}

	coordinator := newIndexCoordinator(func(ctx context.Context, clearFirst, fullPass bool) {
		if searchStore == nil {
			return
		}
		if clearFirst {
			if err := searchStore.Clear(); err != nil {
				slog.Warn("failed to clear search index", "err", err)
				return
			}
		}
		indexSourcesPool = reconcileIndexSources(dir, indexSourcesPool)

		bulkMode := false
		if err := searchStore.BeginBulkIndex(); err != nil {
			slog.Warn("failed to enable bulk FTS indexing", "err", err)
		} else {
			bulkMode = true
		}

		indexSources(ctx, searchStore, indexSourcesPool, fullPass)
		if bulkMode {
			if err := searchStore.EndBulkIndex(); err != nil {
				slog.Warn("failed to finalize bulk FTS indexing", "err", err)
			}
		}
	})
	requestIndex := func(restartIfRunning, clearFirst, fullPass bool) {
		if searchStore == nil {
			return
		}
		coordinator.Request(restartIfRunning, clearFirst, fullPass)
	}

	requestIndex(false, false, true)

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)

	for {
		select {
		case sig := <-sigCh:
			if handleCoreSignal(sig, running, searchStore, func() {
				coordinator.Stop()
				closeIndexSources()
			}, func() { requestIndex(true, false, true) }) {
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
					handleReset(dir, name, &newCfg, searchStore)
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

			requestIndex(false, false, false)
		}
	}
}

// handleCoreSignal runs an immediate index pass for SIGUSR1 and otherwise performs daemon shutdown cleanup.
func handleCoreSignal(sig os.Signal, running map[string]context.CancelFunc, searchStore *SearchStore, stopIndex func(), runIndex func()) bool {
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
		slog.Warn("unknown source", "source", name)
		return
	}
	if available, reason := registry.IsAvailable(name); !available {
		slog.Info("source unavailable, skipping", "source", name, "reason", reason)
		return
	}
	if !desc.RunsCore {
		slog.Info("source enabled for MCP but has no background service", "source", name)
		return
	}
	src := desc.New(dir)
	if src == nil {
		slog.Warn("could not construct source", "source", name)
		return
	}
	cs, ok := src.(core.CoreService)
	if !ok {
		slog.Warn("source marked RunsCore but does not implement CoreService", "source", name)
		return
	}
	if cs.RequiresAuth() && !registry.IsAuthenticated(name, dir) {
		slog.Info("source requires authentication", "source", name, "hint", fmt.Sprintf("run 'mcpyeahyouknowme %s login' first", name))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	running[name] = cancel
	go func() {
		if err := cs.StartCore(ctx); err != nil {
			slog.Error("core service error", "source", name, "err", err)
		}
		delete(running, name)
	}()
}

// Clears one source's local state and indexed rows so daemon-driven resets disable the source without leaving stale search hits behind.
func handleReset(dir, name string, cfg *core.Config, searchStore *SearchStore) {
	src := registry.NewSource(name, dir)
	if src != nil {
		defer src.Close()
		if err := src.Reset(dir); err != nil {
			slog.Error("reset error", "source", name, "err", err)
		}
	}
	if searchStore != nil {
		if err := searchStore.DeleteBySource(name); err != nil {
			slog.Warn("could not clear source from search index", "source", name, "err", err)
		}
	}
	if err := core.SetSourceDisabled(dir, name); err != nil {
		slog.Warn("could not disable source after reset", "source", name, "err", err)
	}
	cfg.Sources[name] = core.SourceConfig{}
}
