package main

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// adaptiveBatchSize returns an embedding batch size scaled to available system
// memory. Returns 4-32, with 16 as the baseline. Falls back to 16 on error.
func adaptiveBatchSize() int {
	freeMB := availableMemoryMB()
	if freeMB <= 0 {
		return 16
	}
	switch {
	case freeMB < 2048:
		return 4
	case freeMB < 4096:
		return 8
	case freeMB < 8192:
		return 16
	default:
		return 32
	}
}

// availableMemoryMB returns approximate free memory in MB on macOS by parsing
// vm_stat output. Returns -1 if detection fails.
func availableMemoryMB() int64 {
	pageSize := int64(16384) // Apple Silicon default; Intel is 4096
	ps, err := exec.Command("sysctl", "-n", "hw.pagesize").Output()
	if err == nil {
		if v, e := strconv.ParseInt(strings.TrimSpace(string(ps)), 10, 64); e == nil {
			pageSize = v
		}
	}

	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return -1
	}

	var freePages, inactivePages int64
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Pages free:") {
			freePages = parseVMStatValue(line)
		} else if strings.HasPrefix(line, "Pages inactive:") {
			inactivePages = parseVMStatValue(line)
		}
	}

	return (freePages + inactivePages) * pageSize / (1024 * 1024)
}

func parseVMStatValue(line string) int64 {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	s := strings.TrimSpace(parts[1])
	s = strings.TrimSuffix(s, ".")
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func daemonRSSBytes(label string) int64 {
	out, err := exec.Command("launchctl", "list", label).Output()
	if err != nil {
		return 0
	}
	pid := parseLaunchctlPID(string(out))
	if pid <= 0 {
		return 0
	}
	psOut, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	return parseProcessRSSBytes(string(psOut))
}

func parseLaunchctlPID(output string) int {
	re := regexp.MustCompile(`"PID"\s*=\s*(\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0
	}
	pid, _ := strconv.Atoi(matches[1])
	return pid
}

func parseProcessRSSBytes(output string) int64 {
	rssKB, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil || rssKB <= 0 {
		return 0
	}
	return rssKB * 1024
}
