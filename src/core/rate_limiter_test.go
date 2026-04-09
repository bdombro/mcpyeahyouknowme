package core

import (
	"testing"
	"time"
)

// Verifies Allow permits exactly limit calls per key inside the window then blocks until old entries age out.
func TestRateLimiter_allowAndBlock(t *testing.T) {
	start := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	now := start
	rl := NewRateLimiter(3, time.Minute)
	rl.SetNowFunc(func() time.Time { return now })

	a1, a2, a3 := rl.Allow("tool_a"), rl.Allow("tool_a"), rl.Allow("tool_a")
	if !a1 || !a2 || !a3 {
		t.Fatal("expected first 3 calls to succeed")
	}
	if rl.Allow("tool_a") {
		t.Fatal("expected 4th call to be blocked")
	}
	if !rl.Allow("tool_b") {
		t.Fatal("expected independent limit per key")
	}

	now = start.Add(2 * time.Minute)
	if !rl.Allow("tool_a") {
		t.Fatal("expected window reset to allow again")
	}
}
