package domain

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func allow(t *testing.T, l *MemoryLimiter, user string) bool {
	t.Helper()
	ok, err := l.Allow(context.Background(), user)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	return ok
}

func TestAttachesAreAllowedUpToTheMaximum(t *testing.T) {
	l := NewMemoryLimiter(3, time.Hour)

	for i := 0; i < 3; i++ {
		if !allow(t, l, "user-1") {
			t.Fatalf("refused attach %d, within the limit of three", i+1)
		}
	}
	if allow(t, l, "user-1") {
		t.Fatal("a fourth attach was allowed past a limit of three")
	}
}

func TestUsersAreCountedSeparately(t *testing.T) {
	l := NewMemoryLimiter(1, time.Hour)

	if !allow(t, l, "user-1") {
		t.Fatal("first attach refused")
	}
	if allow(t, l, "user-1") {
		t.Fatal("second attach for the same user was allowed")
	}
	if !allow(t, l, "user-2") {
		t.Fatal("another user was refused because of the first")
	}
}

func TestTheWindowResets(t *testing.T) {
	l := NewMemoryLimiter(1, 10*time.Millisecond)

	if !allow(t, l, "user-1") {
		t.Fatal("first attach refused")
	}
	if allow(t, l, "user-1") {
		t.Fatal("allowed inside the window")
	}

	time.Sleep(20 * time.Millisecond)
	if !allow(t, l, "user-1") {
		t.Fatal("still refused after the window passed")
	}
}

// A non-positive maximum turns the limit off, which is how the configuration
// disables it — and a nil limiter has to behave the same, because that is what
// a handler constructed without one holds.
func TestANonPositiveMaximumDisablesTheLimit(t *testing.T) {
	l := NewMemoryLimiter(0, time.Hour)
	for i := 0; i < 100; i++ {
		if !allow(t, l, "user-1") {
			t.Fatalf("refused attach %d with the limit disabled", i+1)
		}
	}

	var nilLimiter *MemoryLimiter
	if ok, err := nilLimiter.Allow(context.Background(), "user-1"); !ok || err != nil {
		t.Fatalf("nil limiter refused: %v, %v", ok, err)
	}
}

// Windows are dropped as they expire rather than accumulating one entry per
// account that has ever attached.
func TestExpiredWindowsArePruned(t *testing.T) {
	l := NewMemoryLimiter(1, 10*time.Millisecond)

	for i := 0; i < 50; i++ {
		allow(t, l, "user-"+strconv.Itoa(i))
	}
	time.Sleep(20 * time.Millisecond)
	allow(t, l, "someone-else")

	l.mu.Lock()
	remaining := len(l.windows)
	l.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("%d windows retained, want only the live one", remaining)
	}
}
