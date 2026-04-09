package core

import (
	"sync"
	"time"
)

// RateLimiter enforces a sliding-window per-key call cap (typically per MCP tool name).
type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	calls    map[string][]time.Time
	nowFunc  func() time.Time
}

// NewRateLimiter builds a limiter that allows at most limit calls per key inside each window duration.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		limit:   limit,
		window:  window,
		calls:   make(map[string][]time.Time),
		nowFunc: time.Now,
	}
}

// Allow records one invocation for key and reports whether it is still under the per-window cap.
func (r *RateLimiter) Allow(key string) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.nowFunc()
	cutoff := now.Add(-r.window)
	times := r.calls[key]
	var kept []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.limit {
		r.calls[key] = kept
		return false
	}
	kept = append(kept, now)
	r.calls[key] = kept
	return true
}

// SetNowFunc swaps the clock for tests.
func (r *RateLimiter) SetNowFunc(fn func() time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if fn == nil {
		r.nowFunc = time.Now
		return
	}
	r.nowFunc = fn
}
