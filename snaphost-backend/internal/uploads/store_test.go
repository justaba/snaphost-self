package uploads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T, ttl time.Duration) (*Store, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "uploads")
	s, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, dir
}

func TestPutAndOpenRoundTrip(t *testing.T) {
	s, _ := newStore(t, time.Minute)
	ctx := context.Background()
	payload := []byte("an archive, pretend it is gzip")

	id, size, err := s.Put(ctx, "user-1", bytes.NewReader(payload), 1<<20)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}

	f, owner, gotSize, err := s.Open(ctx, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if owner != "user-1" {
		t.Errorf("owner = %q, want user-1", owner)
	}
	if gotSize != int64(len(payload)) {
		t.Errorf("Open size = %d, want %d", gotSize, len(payload))
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("read back %q, want %q", got, payload)
	}
}

// A body that overruns the limit must leave nothing behind. Otherwise a client
// fills the disk by repeatedly starting an upload it never intends to finish.
func TestAnOversizedUploadIsRefusedAndLeavesNoFile(t *testing.T) {
	s, dir := newStore(t, time.Minute)

	_, _, err := s.Put(context.Background(), "user-1", strings.NewReader(strings.Repeat("x", 100)), 10)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put = %v, want ErrTooLarge", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d files left behind after a refused upload", len(entries))
	}
}

// Exactly at the limit is allowed. The off-by-one here decides whether a
// perfectly legal upload is rejected, and it reads the same either way.
func TestAnUploadExactlyAtTheLimitIsAccepted(t *testing.T) {
	s, _ := newStore(t, time.Minute)

	_, size, err := s.Put(context.Background(), "user-1", strings.NewReader(strings.Repeat("x", 10)), 10)
	if err != nil {
		t.Fatalf("Put at exactly the limit: %v", err)
	}
	if size != 10 {
		t.Fatalf("size = %d, want 10", size)
	}
}

func TestOwnerIsRecorded(t *testing.T) {
	s, _ := newStore(t, time.Minute)
	ctx := context.Background()

	id, _, err := s.Put(ctx, "user-1", strings.NewReader("data"), 1<<20)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	owner, err := s.Owner(ctx, id)
	if err != nil {
		t.Fatalf("Owner: %v", err)
	}
	if owner != "user-1" {
		t.Fatalf("owner = %q, want user-1", owner)
	}
	if _, err := s.Owner(ctx, "no-such-upload"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Owner of an unknown id = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesTheFile(t *testing.T) {
	s, dir := newStore(t, time.Minute)
	ctx := context.Background()

	id, _, err := s.Put(ctx, "user-1", strings.NewReader("data"), 1<<20)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, _, _, err := s.Open(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open after Delete = %v, want ErrNotFound", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("%d files left on disk after Delete", len(entries))
	}
}

// The builder deletes on every path, including ones where expiry got there
// first, so a second delete must be quiet.
func TestDeleteIsIdempotent(t *testing.T) {
	s, _ := newStore(t, time.Minute)
	ctx := context.Background()

	id, _, err := s.Put(ctx, "user-1", strings.NewReader("data"), 1<<20)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if err := s.Delete(ctx, "never-existed"); err != nil {
		t.Fatalf("Delete of an unknown id: %v", err)
	}
}

func TestExpiredUploadsAreRefusedAndSwept(t *testing.T) {
	s, dir := newStore(t, time.Millisecond)
	ctx := context.Background()

	id, _, err := s.Put(ctx, "user-1", strings.NewReader("data"), 1<<20)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	if _, _, _, err := s.Open(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open of an expired upload = %v, want ErrNotFound", err)
	}
	if _, err := s.Owner(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Owner of an expired upload = %v, want ErrNotFound", err)
	}

	if removed := s.Sweep(time.Now()); removed != 1 {
		t.Fatalf("Sweep removed %d, want 1", removed)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("%d files survived the sweep", len(entries))
	}
}

// Files from a previous run have no owner in this run's index. Leaving them
// would be leaving user source code on disk that nothing will ever delete.
func TestStartupEmptiesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(dir, "left-by-a-previous-run")
	if err := os.WriteFile(stale, []byte("someone's source"), 0o600); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	if _, err := NewStore(dir, time.Minute); err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale upload survived startup: %v", err)
	}
}

// An upload whose file was removed underneath the index must read as gone
// rather than as a server error: the caller's recovery is identical.
func TestAMissingFileReadsAsNotFound(t *testing.T) {
	s, dir := newStore(t, time.Minute)
	ctx := context.Background()

	id, _, err := s.Put(ctx, "user-1", strings.NewReader("data"), 1<<20)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, id)); err != nil {
		t.Fatalf("remove file: %v", err)
	}

	if _, _, _, err := s.Open(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open = %v, want ErrNotFound", err)
	}
}

func TestUploadsAreNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not apply Unix file modes")
	}
	s, dir := newStore(t, time.Minute)

	id, _, err := s.Put(context.Background(), "user-1", strings.NewReader("data"), 1<<20)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, id))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("upload mode is %o; another account on the host can read it", mode)
	}
}
