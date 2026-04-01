package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/gsuite"
	"mcpyeahyouknowme/sources/whatsapp"
)

func runInfo() {
	dDir := core.DataDir()

	fmt.Println("┌──────────────────────────────────────────┐")
	fmt.Println("│         mcpyeahyouknowme info            │")
	fmt.Println("└──────────────────────────────────────────┘")
	fmt.Println()

	fmt.Println("\U0001f527 Build")
	fmt.Printf("   Version:    %s\n", BuildVersion)
	fmt.Printf("   Built:      %s\n", BuildTime)
	fmt.Println()

	fmt.Println("\u2699\ufe0f  Core Daemon")
	plist := plistPath()
	if _, err := os.Stat(plist); err == nil {
		ctxLC, cancelLC := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelLC()
		out, err := exec.CommandContext(ctxLC, "launchctl", "list", plistName).Output()
		if err == nil && len(out) > 0 {
			fmt.Println("   Status:     running")
		} else {
			fmt.Println("   Status:     installed (not running)")
		}
		fmt.Printf("   Plist:      %s\n", plist)
		fmt.Printf("   Logs:       %s\n", filepath.Join(dDir, "core.log"))
	} else {
		fmt.Println("   Status:     not installed")
	}
	if core.IsNetworkAvailable() {
		fmt.Println("   Network:    online")
	} else {
		fmt.Println("   Network:    offline (sync paused)")
	}
	fmt.Println()

	fmt.Println("\U0001f4c1 Data")
	fmt.Printf("   Directory:  %s\n", dDir)
	if info, err := os.Stat(dDir); err == nil && info.IsDir() {
		fmt.Println("   Status:     initialized")
	} else {
		fmt.Println("   Status:     not initialized (run 'mcpyeahyouknowme whatsapp login')")
	}
	fmt.Println()

	fmt.Println("\U0001f4f2 WhatsApp")
	for _, line := range whatsapp.InfoLines(dDir) {
		fmt.Println(line)
	}
	fmt.Println()

	fmt.Println("\U0001f537 Google Suite")
	for _, line := range gsuite.InfoLines(dDir) {
		fmt.Println(line)
	}
	fmt.Println()
}
