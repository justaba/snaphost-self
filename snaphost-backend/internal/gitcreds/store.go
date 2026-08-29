// Package gitcreds holds the short-lived credential a private-repository
// deploy clones with.
//
// Only the opaque id travels through the saga job, the deploy_sagas row and the
// build queue; the token itself never leaves this store, and the builder
// deletes it the moment the clone attempt ends, successful or not. The TTL is
// the backstop for a build that never runs.
//
// # Why moving off Redis is a security improvement, not just one fewer daemon
//
// Redis here was configured with appendonly persistence, so every credential
// written to it was appended to a file on disk — a user's git token, in
// plaintext, in a log that is copied by any backup of that volume. Nothing read
// it back: the token's whole life is measured in minutes and the builder
// deletes it. It was written to disk for no reason at all.
//
// A map in the process that uses it cannot outlive the process, and never
// touches a disk.
package gitcreds

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound reports a credential id that does not exist: expired, already
// consumed, or never stored.
var ErrNotFound = errors.New("git credential not found")

// Credential is what one clone needs.
type Credential struct {
	Username string
	Token    string
	UserID   string
}

// Store keeps credentials in memory with an expiry.
type Store struct {
	mu    sync.Mutex
	items map[string]entry
}

type entry struct {
	cred    Credential
	expires time.Time
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{items: make(map[string]entry)} }

// Put stores a credential under a fresh id and returns it.
func (s *Store) Put(_ context.Context, userID, username, token string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	id := uuid.NewString()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[id] = entry{
		cred:    Credential{Username: username, Token: token, UserID: userID},
		expires: time.Now().Add(ttl),
	}
	return id, nil
}

// Get returns the credential. The caller must Delete it after the clone
// attempt regardless of outcome.
func (s *Store) Get(_ context.Context, credentialID string) (*Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.items[credentialID]
	if !ok || time.Now().After(e.expires) {
		return nil, ErrNotFound
	}
	cred := e.cred
	return &cred, nil
}

// Delete removes a credential. Deleting one that is already gone is not an
// error — the builder's cleanup runs on every path, including the ones where
// expiry got there first.
func (s *Store) Delete(_ context.Context, credentialID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, credentialID)
	return nil
}

// Sweep drops expired credentials and reports how many went.
//
// Expired entries are already refused by Get, so this is not about correctness:
// it is about how long a token stays in the process's memory after it stopped
// being usable. Redis freed the key on expiry; on a map, something has to.
func (s *Store) Sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for id, e := range s.items {
		if now.After(e.expires) {
			delete(s.items, id)
			removed++
		}
	}
	return removed
}

// Run sweeps on a ticker until ctx is cancelled.
func (s *Store) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.Sweep(now)
		}
	}
}
