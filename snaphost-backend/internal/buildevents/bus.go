// Package buildevents carries the one signal the saga waits on: whether a
// build finished, and with what.
//
// It replaces a Redis pub/sub channel, build-events:{deploy_id}, which existed
// because the build pipeline and the saga were separate processes. It is
// deliberately not the log bus: log lines are a stream of text for a person to
// read and may be dropped for a slow reader, while this is a state transition
// the saga acts on and must not be.
//
// # Why the last event is retained
//
// Enqueueing the build and waiting for it are two separate saga steps, so a
// build can finish between them. Pub/sub has no retention, so the event went
// to nobody and the saga waited out its whole build timeout before recovering.
// That was survivable — the orchestrator re-reads the persisted image ref
// before subscribing, which is what actually closed the race — but the window
// between that check and the subscription was real.
//
// Retaining the terminal event closes it: a waiter that arrives after the
// build finished is answered immediately instead of waiting for a timeout it
// will recover from anyway.
package buildevents

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Type identifies which transition an event reports.
type Type string

const (
	// Started is emitted when the pipeline begins building. Nothing waits on
	// it; it exists so the log stream has a marker.
	Started Type = "started"
	// Completed is emitted after a successful image push.
	Completed Type = "completed"
	// Failed is emitted when the pipeline gives up.
	Failed Type = "failed"
)

// Event is one build lifecycle transition.
type Event struct {
	Type     Type
	DeployID string
	ImageRef string
	// Port is the application listen port resolved from EXPOSE, or from a
	// language heuristic when there is none. Zero means the pipeline could not
	// tell and the waiter should fall back to its configured default.
	Port      int
	CommitSHA string
	Reason    string
	Timestamp time.Time
}

// Terminal reports whether an event ends a build.
func (e Event) Terminal() bool { return e.Type == Completed || e.Type == Failed }

// defaultRetained caps how many finished builds are remembered. It only has to
// cover the gap between a build ending and its saga step running, so this is
// generous by a wide margin.
const defaultRetained = 64

// ErrClosed reports that the waiter's context ended before an event arrived.
// A timeout reaches the caller as the context's own error.
var ErrClosed = errors.New("build event wait ended")

// Bus delivers build outcomes to whoever is waiting, and remembers the last
// terminal outcome per deploy for whoever has not started waiting yet.
type Bus struct {
	mu       sync.Mutex
	waiters  map[string]map[uint64]chan Event
	last     map[string]Event
	order    map[string]uint64
	nextID   uint64
	nextSeq  uint64
	retained int
}

// New creates a bus. A non-positive retained count takes the default.
func New(retained int) *Bus {
	if retained <= 0 {
		retained = defaultRetained
	}
	return &Bus{
		waiters:  make(map[string]map[uint64]chan Event),
		last:     make(map[string]Event),
		order:    make(map[string]uint64),
		retained: retained,
	}
}

// Publish delivers an event to every waiter on that deploy, and retains it if
// it is terminal.
//
// Sends are buffered and non-blocking, so a waiter that has already given up —
// its context expired between the select and here — costs a dropped send
// rather than a stuck publisher. The build pipeline is the caller.
func (b *Bus) Publish(ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if ev.Terminal() {
		b.retainLocked(ev)
	}
	for _, ch := range b.waiters[ev.DeployID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Wait blocks until a terminal event for the deploy arrives, the timeout
// elapses, or ctx ends. A build that already finished is answered from what
// was retained, without waiting.
//
// Non-terminal events are skipped rather than returned: "started" is not an
// outcome, and returning it would make the saga treat a build in progress as
// a build that ended.
func (b *Bus) Wait(ctx context.Context, deployID string, timeout time.Duration) (Event, error) {
	b.mu.Lock()
	if ev, ok := b.last[deployID]; ok {
		b.mu.Unlock()
		return ev, nil
	}
	// Registering the waiter under the same lock that Publish takes is what
	// makes "check the retained event, then subscribe" atomic. Doing it in two
	// steps would reopen the window this package exists to close.
	ch := make(chan Event, 4)
	b.nextID++
	id := b.nextID
	if b.waiters[deployID] == nil {
		b.waiters[deployID] = make(map[uint64]chan Event)
	}
	b.waiters[deployID][id] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if m := b.waiters[deployID]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(b.waiters, deployID)
			}
		}
	}()

	waitCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	for {
		select {
		case <-waitCtx.Done():
			return Event{}, waitCtx.Err()
		case ev, ok := <-ch:
			if !ok {
				return Event{}, ErrClosed
			}
			if ev.Terminal() {
				return ev, nil
			}
		}
	}
}

// Forget drops the retained outcome for a deploy. Waiters are untouched: their
// channels belong to their own Wait call.
func (b *Bus) Forget(deployID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.last, deployID)
	delete(b.order, deployID)
}

// retainLocked stores a terminal event, evicting the oldest when full.
func (b *Bus) retainLocked(ev Event) {
	b.nextSeq++
	if _, exists := b.last[ev.DeployID]; !exists && len(b.last) >= b.retained {
		var oldestID string
		var oldest uint64
		first := true
		for id, seq := range b.order {
			if first || seq < oldest {
				oldestID, oldest, first = id, seq, false
			}
		}
		if oldestID != "" {
			delete(b.last, oldestID)
			delete(b.order, oldestID)
		}
	}
	b.last[ev.DeployID] = ev
	b.order[ev.DeployID] = b.nextSeq
}
