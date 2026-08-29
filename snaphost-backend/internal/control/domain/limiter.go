package domain

import (
	"context"
	"sync"
	"time"
)

// MemoryLimiter is a fixed-window per-user attach limiter.
//
// Attach is a rare, deliberate action, so a coarse window is enough — this
// exists to cap the DNS lookups and certificate issuance attempts one account
// can trigger, not to shape traffic.
//
// It was an INCR and an EXPIRE against Redis. Losing the counter on a restart
// is the entire behavioural difference, and for a gate on a deliberate action
// by one operator that is not worth a daemon.
type MemoryLimiter struct {
	max    int
	window time.Duration

	mu      sync.Mutex
	windows map[string]window
}

type window struct {
	count int
	// resets is when this user's window ends. The first hit in a window owns
	// its expiry, which is what the Redis version's "set the TTL only when
	// INCR returned 1" was doing.
	resets time.Time
}

// NewMemoryLimiter allows max attaches per user per window.
func NewMemoryLimiter(max int, w time.Duration) *MemoryLimiter {
	return &MemoryLimiter{max: max, window: w, windows: make(map[string]window)}
}

// Allow reports whether this user may attach another domain right now.
//
// A non-positive maximum disables the limit, which is how the configuration
// turns it off and how a nil limiter behaves.
func (l *MemoryLimiter) Allow(_ context.Context, userID string) (bool, error) {
	if l == nil || l.max <= 0 {
		return true, nil
	}

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.windows[userID]
	if !ok || now.After(w.resets) {
		w = window{resets: now.Add(l.window)}
	}
	w.count++
	l.windows[userID] = w

	// Expired windows are only dropped when someone attaches, which is the
	// cheapest place for it: the map has one entry per account that has tried
	// in the last hour, and a platform with one operator will never have two.
	l.pruneLocked(now)

	return w.count <= l.max, nil
}

func (l *MemoryLimiter) pruneLocked(now time.Time) {
	for id, w := range l.windows {
		if now.After(w.resets) {
			delete(l.windows, id)
		}
	}
}
