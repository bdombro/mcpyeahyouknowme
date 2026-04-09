package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

var sensitiveAuditArgKeys = map[string]struct{}{
	"message": {}, "body": {}, "base64": {}, "media_path": {}, "path": {}, "query": {},
}

type auditLine struct {
	TS          string `json:"ts"`
	Tool        string `json:"tool"`
	ArgsSummary string `json:"args_summary"`
}

// AuditLogger appends one JSON line per MCP tool call to mcp-audit.log, trimming like core.log when large.
type AuditLogger struct {
	mu      sync.Mutex
	path    string
	maxLine int
}

// NewAuditLogger opens the audit log path under dataDir, creating parent directories as needed.
func NewAuditLogger(dataDir string) (*AuditLogger, error) {
	path := filepath.Join(dataDir, "mcp-audit.log")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	return &AuditLogger{path: path, maxLine: 200}, nil
}

// Log writes one audit record for a tool invocation. It is safe to call with a nil receiver.
func (a *AuditLogger) Log(tool string, args map[string]interface{}) {
	if a == nil {
		return
	}
	sum := summarizeArgsForAudit(args, a.maxLine)
	line := auditLine{TS: time.Now().UTC().Format(time.RFC3339Nano), Tool: tool, ArgsSummary: sum}
	data, err := json.Marshal(line)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = TrimLogFilePath(a.path, LogTrimThresholdBytes, LogKeepTailBytes)
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// summarizeArgsForAudit builds a compact JSON-ish summary, redacting sensitive argument keys by length only.
func summarizeArgsForAudit(args map[string]interface{}, maxTotal int) string {
	if len(args) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		v := args[k]
		lk := strings.ToLower(k)
		if _, redact := sensitiveAuditArgKeys[lk]; redact {
			s := fmt.Sprintf("%v", v)
			fmt.Fprintf(&b, "%q:[redacted len=%d]", k, len(s))
			continue
		}
		s := fmt.Sprintf("%v", v)
		if len(s) > 80 {
			s = s[:80] + "…"
		}
		fmt.Fprintf(&b, "%q:%q", k, s)
	}
	b.WriteByte('}')
	out := b.String()
	if len(out) > maxTotal {
		out = out[:maxTotal] + "…"
	}
	return out
}
