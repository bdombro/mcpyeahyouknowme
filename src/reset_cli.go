package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/registry"
)

var (
	resetAllRunner                = runResetAll
	resetAllStdin       io.Reader = os.Stdin
	resetAllStdout      io.Writer = os.Stdout
	resetAllStderr      io.Writer = os.Stderr
	resetAllDescriptors           = func() []registry.Descriptor { return registry.All }
	resetAllSearchReset           = func(dataDir string) error {
		return core.DefaultReset(dataDir, []string{
			"search.db",
			"search.db-wal",
			"search.db-shm",
		})
	}
	resetAllDaemonPID     = coreDaemonPID
	resetAllRestartDaemon = restartInstalledDaemon
	resetAllSaveConfig    = func(dataDir string, cfg core.Config) error {
		return core.SaveConfig(dataDir, cfg)
	}
)

// runResetAll prompts before clearing all source-owned data so users do not accidentally wipe every connection and local index.
func runResetAll(dataDir string) {
	fmt.Fprint(resetAllStdout, "This will reset ALL source connections and data. Are you sure? (yes/no): ")

	var response string
	if _, err := fmt.Fscanln(resetAllStdin, &response); err != nil || strings.ToLower(response) != "yes" {
		fmt.Fprintln(resetAllStdout, "Cancelled.")
		return
	}

	doResetAll(dataDir)
}

// doResetAll resets every registered source, clears the global search index, and rewrites config.json to the normalized disabled state.
func doResetAll(dataDir string) {
	for _, desc := range resetAllDescriptors() {
		src := desc.New(dataDir)
		if err := src.Reset(dataDir); err != nil {
			fmt.Fprintf(resetAllStderr, "Warning: %s reset: %v\n", desc.Name, err)
		} else {
			fmt.Fprintf(resetAllStdout, "  Reset %s\n", desc.Name)
		}
		closeResetSource(desc.Name, src)
	}

	if err := resetAllSearchReset(dataDir); err != nil {
		fmt.Fprintf(resetAllStderr, "Warning: search index reset: %v\n", err)
	} else {
		fmt.Fprintln(resetAllStdout, "  Reset search index")
	}

	if err := resetAllSaveConfig(dataDir, core.Config{Sources: map[string]core.SourceConfig{}}); err != nil {
		fmt.Fprintf(resetAllStderr, "Warning: could not reset config.json: %v\n", err)
	} else {
		fmt.Fprintln(resetAllStdout, "  Reset config.json")
	}
	if resetAllDaemonPID() > 0 {
		if err := resetAllRestartDaemon(); err != nil {
			fmt.Fprintf(resetAllStderr, "Warning: could not restart daemon after reset: %v\n", err)
		} else {
			fmt.Fprintln(resetAllStdout, "  Restarted daemon")
		}
	}

	fmt.Fprintln(resetAllStdout, "All connections and data reset.")
}

// closeResetSource closes one source while converting panics into warnings so one broken implementation does not abort a full reset.
func closeResetSource(name string, src interface{ Close() error }) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(resetAllStderr, "Warning: %s close panic: %v\n", name, r)
		}
	}()

	if err := src.Close(); err != nil {
		fmt.Fprintf(resetAllStderr, "Warning: %s close: %v\n", name, err)
	}
}
