package logs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"snaphost/internal/logbus"
)

type fakeArchive struct {
	blob []byte
	err  error
}

func (f fakeArchive) LogTail(context.Context, uuid.UUID) ([]byte, error) { return f.blob, f.err }

func TestReadServesLiveHistoryFromTheBus(t *testing.T) {
	bus := logbus.New(0, 0)
	id := uuid.New().String()
	bus.Publish(id, logbus.Line{Stage: "build", Text: "one"})
	bus.Publish(id, logbus.Line{Stage: "build", Text: "two"})

	r := NewReader(bus, fakeArchive{blob: []byte(`[{"text":"stale archive"}]`)})

	entries, next, err := r.Read(context.Background(), id, "", 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 || entries[0].Line.Text != "one" || entries[1].Line.Text != "two" {
		t.Fatalf("entries = %+v", entries)
	}
	if next == "" {
		t.Error("no cursor returned")
	}
}

// Once the bus has dropped a deploy, the archive is what is left. This is the
// path a restart puts every failed deploy on.
func TestReadFallsBackToTheArchive(t *testing.T) {
	bus := logbus.New(0, 0)
	id := uuid.New().String()

	r := NewReader(bus, fakeArchive{blob: []byte(`[{"text":"first"},{"text":"second"}]`)})

	entries, next, err := r.Read(context.Background(), id, "", 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 || entries[0].Line.Text != "first" {
		t.Fatalf("entries = %+v", entries)
	}

	// The cursor has to keep working against the archive, or a client polling
	// across the moment the bus forgets would loop on the same two lines.
	again, _, err := r.Read(context.Background(), id, next, 10)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("re-reading with the end cursor returned %+v", again)
	}
}

func TestArchivePages(t *testing.T) {
	r := NewReader(logbus.New(0, 0), fakeArchive{
		blob: []byte(`[{"text":"a"},{"text":"b"},{"text":"c"}]`),
	})
	id := uuid.New().String()

	first, cursor, err := r.Read(context.Background(), id, "", 2)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(first) != 2 || first[1].Line.Text != "b" {
		t.Fatalf("first page = %+v", first)
	}

	second, _, err := r.Read(context.Background(), id, cursor, 2)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(second) != 1 || second[0].Line.Text != "c" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestReadWithNoLiveHistoryAndNoArchive(t *testing.T) {
	r := NewReader(logbus.New(0, 0), fakeArchive{})

	entries, next, err := r.Read(context.Background(), uuid.New().String(), "", 10)
	if err != nil || len(entries) != 0 || next != "" {
		t.Fatalf("Read = %+v, %q, %v; want empty", entries, next, err)
	}
}

func TestReadWithoutAnArchiveConfigured(t *testing.T) {
	r := NewReader(logbus.New(0, 0), nil)

	if _, _, err := r.Read(context.Background(), uuid.New().String(), "", 10); err != nil {
		t.Fatalf("Read: %v", err)
	}
}

func TestArchiveErrorsSurface(t *testing.T) {
	want := errors.New("database is gone")
	r := NewReader(logbus.New(0, 0), fakeArchive{err: want})

	if _, _, err := r.Read(context.Background(), uuid.New().String(), "", 10); !errors.Is(err, want) {
		t.Fatalf("Read error = %v, want %v", err, want)
	}
}

// A deploy id that is not a UUID cannot have an archive keyed by one. It must
// read as empty rather than erroring: the bus is keyed by the raw string and
// answered first.
func TestANonUUIDDeployIDReadsAsEmpty(t *testing.T) {
	r := NewReader(logbus.New(0, 0), fakeArchive{blob: []byte(`[{"text":"x"}]`)})

	entries, _, err := r.Read(context.Background(), "not-a-uuid", "", 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Read = %+v, %v; want empty and no error", entries, err)
	}
}
