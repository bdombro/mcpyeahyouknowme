package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

// runCore starts data source core services with config polling (10s interval).
func runCore() {
	dir := core.DataDir()
	cfg := loadConfig(dir)

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

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigCh:
			for _, cancel := range running {
				cancel()
			}
			return
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
		}
	}
}

// startSource constructs the source, checks auth, and starts its CoreService.
func startSource(dir, name string, running map[string]context.CancelFunc) {
	src := registry.NewSource(name, dir)
	if src == nil {
		fmt.Fprintf(os.Stderr, "Warning: unknown source %q\n", name)
		return
	}
	cs, ok := src.(core.CoreService)
	if !ok {
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

// handleReset calls source.Reset(), removes the config entry, and saves config.
func handleReset(dir, name string, cfg *core.Config) {
	src := registry.NewSource(name, dir)
	if src != nil {
		if err := src.Reset(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Reset error for %s: %v\n", name, err)
		}
	}
	delete(cfg.Sources, name)
	saveConfig(dir, *cfg)
}
