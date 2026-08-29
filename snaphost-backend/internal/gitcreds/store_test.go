package gitcreds

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPutAndGetRoundTrip(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	id, err := s.Put(ctx, "user-1", "git", "a-token", time.Minute)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	cred, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cred.Username != "git" || cred.Token != "a-token" || cred.UserID != "user-1" {
		t.Fatalf("credential = %+v", cred)
	}
}

// Get hands back a copy. A caller that scrubs what it received must not be
// scrubbing the stored entry — or the retry that follows finds a blank token
// and fails with a confusing "authentication failed".
func TestGetReturnsACopy(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	id, err := s.Put(ctx, "user-1", "git", "a-token", time.Minute)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	first, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	first.Token = "scrubbed"

	second, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if second.Token != "a-token" {
		t.Fatalf("stored token became %q after a caller modified its copy", second.Token)
	}
}

func TestUnknownCredential(t *testing.T) {
	s := NewStore()
	if _, err := s.Get(context.Background(), "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesTheCredential(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	id, err := s.Put(ctx, "user-1", "git", "a-token", time.Minute)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}

	// The builder's cleanup runs on every path, including ones where the
	// credential is already gone.
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestExpiredCredentialsAreRefused(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	id, err := s.Put(ctx, "user-1", "git", "a-token", time.Millisecond)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of an expired credential = %v, want ErrNotFound", err)
	}
}

// Expiry already refuses the credential, so sweeping is about how long a token
// stays in memory after it stopped being usable — which for a secret is the
// whole point.
func TestSweepDropsExpiredCredentials(t *testing.T) {
	s := NewStore()
	ctx := context.Background()

	if _, err := s.Put(ctx, "user-1", "git", "expired", time.Millisecond); err != nil {
		t.Fatalf("Put: %v", err)
	}
	live, err := s.Put(ctx, "user-1", "git", "live", time.Hour)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	if removed := s.Sweep(time.Now()); removed != 1 {
		t.Fatalf("Sweep removed %d, want 1", removed)
	}
	s.mu.Lock()
	remaining := len(s.items)
	s.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("%d credentials remain, want 1", remaining)
	}
	if _, err := s.Get(ctx, live); err != nil {
		t.Fatalf("the live credential was swept: %v", err)
	}
}

func TestPutWithoutATTLStillExpires(t *testing.T) {
	s := NewStore()

	id, err := s.Put(context.Background(), "user-1", "git", "a-token", 0)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	s.mu.Lock()
	e := s.items[id]
	s.mu.Unlock()
	if e.expires.IsZero() || time.Until(e.expires) <= 0 {
		t.Fatalf("a zero TTL produced expiry %s; it must fall back to a real one", e.expires)
	}
}
