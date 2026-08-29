// Package uploads holds source archives between the request that uploads one
// and the build that consumes it.
//
// It was a Redis hash with the archive bytes in a field, which is the single
// worst place this platform kept anything. A 50 MB tar.gz was read fully into
// the API process, copied into a Redis command, held in Redis's heap for the
// TTL, then read back into the builder's heap to be unpacked — four copies of
// the same bytes, two of them in a process whose whole purpose here was to be
// small. On the class of machine this fork targets that cost more than the Go
// runtime it was next to.
//
// It is a file now, streamed in and streamed out. Nothing holds the archive.
//
// # No durability, deliberately
//
// The index of who owns which upload is in memory, and the directory is
// emptied at startup. An upload does not survive a restart — which the
// fifteen-minute TTL already implied, and which is the same bargain the queues
// make: the durable thing is the deploy, and an archive deploy interrupted
// before its build simply fails.
package uploads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound reports an upload id that does not exist: never stored, expired,
// or already consumed by a build.
var ErrNotFound = errors.New("upload not found")

// ErrTooLarge reports a body that exceeded the caller's limit mid-stream. It is
// distinct from ErrNotFound because it is the client's fault and answerable
// with a 413.
var ErrTooLarge = errors.New("upload exceeds the maximum size")

// Store keeps uploaded archives on disk and their ownership in memory.
type Store struct {
	dir string
	ttl time.Duration

	mu    sync.Mutex
	items map[string]item
}

type item struct {
	owner   string
	path    string
	size    int64
	expires time.Time
}

// NewStore prepares the upload directory and returns a store.
//
// The directory is emptied first. Anything in it belongs to a previous run of
// this process, and this run has no index for it — leaving those files would
// be leaving user source code on disk that nothing will ever delete.
func NewStore(dir string, ttl time.Duration) (*Store, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read upload directory: %w", err)
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
	return &Store{dir: dir, ttl: ttl, items: make(map[string]item)}, nil
}

// Put streams an archive to disk and returns its id and size.
//
// max bounds what is written. The check is on the bytes actually copied rather
// than on a declared length, because a chunked body declares nothing — and the
// file is removed before the error is returned, so a client cannot fill the
// disk by repeatedly starting an oversized upload.
func (s *Store) Put(_ context.Context, userID string, r io.Reader, max int64) (string, int64, error) {
	id := uuid.NewString()
	path := filepath.Join(s.dir, id)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create upload file: %w", err)
	}

	// One byte past the limit, so hitting it exactly is not mistaken for
	// overflowing it.
	limited := io.LimitReader(r, max+1)
	size, copyErr := io.Copy(f, limited)
	closeErr := f.Close()

	switch {
	case copyErr != nil:
		_ = os.Remove(path)
		return "", 0, fmt.Errorf("write upload: %w", copyErr)
	case closeErr != nil:
		_ = os.Remove(path)
		return "", 0, fmt.Errorf("close upload: %w", closeErr)
	case size > max:
		_ = os.Remove(path)
		return "", 0, ErrTooLarge
	}

	s.mu.Lock()
	s.items[id] = item{owner: userID, path: path, size: size, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()

	return id, size, nil
}

// Open returns the archive as a reader, with the id of the account that
// uploaded it. The caller closes the reader and is expected to Delete the
// upload afterwards; expiry is the backstop.
func (s *Store) Open(_ context.Context, uploadID string) (*os.File, string, int64, error) {
	s.mu.Lock()
	it, ok := s.items[uploadID]
	s.mu.Unlock()

	if !ok || time.Now().After(it.expires) {
		return nil, "", 0, ErrNotFound
	}
	f, err := os.Open(it.path)
	if err != nil {
		// The index says it exists and the disk disagrees. Treat it as gone
		// rather than as a server error: the caller's recovery is the same.
		return nil, "", 0, ErrNotFound
	}
	return f, it.owner, it.size, nil
}

// Owner reports who uploaded an archive, without opening it. Used to reject a
// deploy referencing someone else's upload before any build is queued.
func (s *Store) Owner(_ context.Context, uploadID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	it, ok := s.items[uploadID]
	if !ok || time.Now().After(it.expires) {
		return "", ErrNotFound
	}
	return it.owner, nil
}

// Delete removes an archive. Deleting one that is already gone is not an
// error: the builder deletes after unpack whether the unpack succeeded or not,
// and expiry may have got there first.
func (s *Store) Delete(_ context.Context, uploadID string) error {
	s.mu.Lock()
	it, ok := s.items[uploadID]
	delete(s.items, uploadID)
	s.mu.Unlock()

	if !ok {
		return nil
	}
	if err := os.Remove(it.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove upload: %w", err)
	}
	return nil
}

// Sweep removes expired archives and reports how many went. Redis did this
// with a per-key TTL; on a filesystem it has to be someone's job.
func (s *Store) Sweep(now time.Time) int {
	s.mu.Lock()
	var stale []item
	for id, it := range s.items {
		if now.After(it.expires) {
			stale = append(stale, it)
			delete(s.items, id)
		}
	}
	s.mu.Unlock()

	for _, it := range stale {
		_ = os.Remove(it.path)
	}
	return len(stale)
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
