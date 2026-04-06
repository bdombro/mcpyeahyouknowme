package main

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var coreDaemonStatPath = os.Stat
var coreDaemonLaunchctlList = func(label string) ([]byte, error) {
	return exec.Command("launchctl", "list", label).Output()
}

// adaptiveBatchSize returns an embedding batch size scaled to current system
// headroom and capped by the daemon's RSS so long reindexes stay responsive.
func adaptiveBatchSize() int {
	return embeddingBatchSizeForSystem(availableMemoryMB(), daemonRSSBytes(plistName)/(1024*1024))
}

// embeddingBatchSizeForSystem combines free-memory scaling with a daemon-RSS cap
// so batch growth stops once the indexer itself is already consuming too much RAM.
func embeddingBatchSizeForSystem(freeMB, daemonRSSMB int64) int {
	batchSize := embeddingBatchSizeForMemoryMB(freeMB)
	rssCap := embeddingBatchSizeForRSSMB(daemonRSSMB)
	if rssCap > 0 && rssCap < batchSize {
		return rssCap
	}
	return batchSize
}

// embeddingBatchSizeForMemoryMB maps available memory to a safe embedding batch
// size so high-memory Macs can complete large reindexes in reasonable time.
func embeddingBatchSizeForMemoryMB(freeMB int64) int {
	if freeMB <= 0 {
		return 16
	}
	switch {
	case freeMB < 2048:
		return 8
	case freeMB < 4096:
		return 16
	case freeMB < 8192:
		return 32
	case freeMB < 16384:
		return 64
	default:
		return 128
	}
}

// embeddingBatchSizeForRSSMB caps batch size based on the daemon's current RSS
// so embedding passes back off before the process makes the machine sluggish.
func embeddingBatchSizeForRSSMB(rssMB int64) int {
	if rssMB <= 0 {
		return 128
	}
	switch {
	case rssMB >= 16384:
		return 8
	case rssMB >= 12288:
		return 16
	case rssMB >= 8192:
		return 32
	case rssMB >= 4096:
		return 64
	default:
		return 128
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

// parseVMStatValue extracts one vm_stat page count from a labeled line so memory math can proceed.
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

// daemonRSSBytes returns the daemon RSS in bytes by resolving the LaunchAgent PID and querying `ps`.
func daemonRSSBytes(label string) int64 {
	out, err := coreDaemonLaunchctlList(label)
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

// coreDaemonPID returns the LaunchAgent PID when the installed core daemon is
// running, or zero when the plist is absent or launchctl reports no process.
func coreDaemonPID() int {
	if _, err := coreDaemonStatPath(plistPath()); err != nil {
		return 0
	}
	out, err := coreDaemonLaunchctlList(plistName)
	if err != nil || len(out) == 0 {
		return 0
	}
	return parseLaunchctlPID(string(out))
}

// parseLaunchctlPID extracts the numeric PID from `launchctl list` output for a loaded service.
func parseLaunchctlPID(output string) int {
	re := regexp.MustCompile(`"PID"\s*=\s*(\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0
	}
	pid, _ := strconv.Atoi(matches[1])
	return pid
}

// parseProcessRSSBytes converts `ps` RSS kilobytes into bytes, or zero when parsing fails.
func parseProcessRSSBytes(output string) int64 {
	rssKB, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil || rssKB <= 0 {
		return 0
	}
	return rssKB * 1024
}
