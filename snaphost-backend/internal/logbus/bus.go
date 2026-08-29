// Package logbus is the in-process meeting point for deploy log lines.
//
// It replaces Redis, which held two things under two key schemes: a pub/sub
// channel `logs:{deploy_id}` that the WebSocket endpoint subscribed to, and a
// capped stream `logs-history:{deploy_id}` that the HTTP history endpoint read
// back. Four packages published to the first — the control plane's saga, the
// build pipeline, the runtime, and the generator — and they used a message
// broker to reach each other because they were four processes. They are one
// now, so the broker was a network hop between two goroutines.
//
// # What is kept and what is dropped
//
// The bounded history is kept, at the same 1000 lines per deploy that Redis's
// MAXLEN enforced, and the cursor contract of the HTTP endpoint is unchanged:
// an opaque string that is passed back as `since`. What is not kept is
// durability. Redis persisted with appendonly, so history survived a restart;
// this does not. That is a deliberate trade — see [Bus.Tail], which is how the
// part worth keeping is kept: when a deploy fails, its last lines are written
// to the database, because a failed build's output is the thing anyone comes
// back for. A successful build's output is read while it scrolls past and
// never again.
//
// # Slow subscribers
//
// A subscriber that stops reading must not stall a build. Redis pub/sub drops
// messages for a consumer that falls behind, and so does this: each subscriber
// gets a buffered channel and a full buffer means the line is dropped for that
// subscriber only. Blocking instead would make a browser tab that stopped
// draining its WebSocket able to halt the build pipeline.
package logbus

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

// Defaults matching what Redis was configured with.
const (
	// DefaultHistory is how many lines are retained per deploy. The same
	// value the `logs-history:` stream used as MAXLEN.
	DefaultHistory = 1000
	// DefaultTopics caps how many deploys hold history at once, and is what
	// bounds this package's memory rather than any explicit release: a deploy
	// keeps its history after it ends, because reloading the page just after a
	// build is exactly when someone wants to read it. Sixty-four full topics is
	// under 10 MB, and a typical build is a few hundred lines rather than the
	// thousand retained.
	DefaultTopics = 64
	// subscriberBuffer is how far behind a subscriber may fall before its
	// lines start being dropped.
	subscriberBuffer = 256
)

// Line is one structured log line. The JSON tags match what the WebSocket and
// the history endpoint already emitted, because the browser decodes them.
type Line struct {
	DeployID  string    `json:"deploy_id"`
	Stage     string    `json:"stage"`
	Text      string    `json:"text"`
	Level     string    `json:"level"`
	Timestamp time.Time `json:"timestamp"`
}

// Entry is a history line with the cursor identifying it. ID is a string
// because it used to be a Redis stream id and the HTTP contract passes it back
// unchanged as `since`; it happens to be a decimal sequence number now.
type Entry struct {
	ID   string `json:"id"`
	Line Line   `json:"line"`
}

// Bus fans log lines out to live subscribers and retains a bounded history.
type Bus struct {
	mu         sync.Mutex
	topics     map[string]*topic
	maxLines   int
	maxTopics  int
	nextSubID  uint64
	nextTopicN uint64
}

type topic struct {
	// lines is the retained history, oldest first. firstSeq is the sequence
	// number of lines[0]; a line's own number is firstSeq plus its index, so
	// cursors stay meaningful after eviction rather than shifting.
	lines    []Line
	firstSeq int
	subs     map[uint64]chan Line
	// order records when this topic was last written to, for evicting the
	// least recently used one when maxTopics is exceeded.
	order uint64
}

// New creates a bus. Non-positive arguments take the defaults.
func New(maxLines, maxTopics int) *Bus {
	if maxLines <= 0 {
		maxLines = DefaultHistory
	}
	if maxTopics <= 0 {
		maxTopics = DefaultTopics
	}
	return &Bus{
		topics:    make(map[string]*topic),
		maxLines:  maxLines,
		maxTopics: maxTopics,
	}
}

// Publish records a line and delivers it to every live subscriber.
//
// It never blocks and never fails. Publishing a log line is on the build
// pipeline's critical path, and there is nothing useful a caller could do with
// an error from it — which is why the Redis publishers it replaces already
// logged their errors and returned nil.
func (b *Bus) Publish(deployID string, line Line) {
	line.DeployID = deployID
	if line.Timestamp.IsZero() {
		line.Timestamp = time.Now().UTC()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	t := b.topicLocked(deployID)
	t.lines = append(t.lines, line)
	if len(t.lines) > b.maxLines {
		drop := len(t.lines) - b.maxLines
		t.lines = t.lines[drop:]
		t.firstSeq += drop
	}

	// Delivery happens under the same lock that unsubscribing takes, and that
	// is load-bearing rather than lazy. Copying the channels out and sending
	// after unlocking looks cheaper and is wrong: a subscriber unsubscribing
	// in that window closes a channel this loop is about to send on, which is
	// a panic, not a dropped line. The race detector found it on the first
	// concurrent run.
	//
	// It costs nothing here because every send is non-blocking. A subscriber
	// that is behind cannot hold the lock — its line is dropped instead, and
	// the history it can re-read is what covers the gap.
	for _, ch := range t.subs {
		select {
		case ch <- line:
		default:
		}
	}
}

// Subscribe returns a channel of lines published from now on, and a function
// that stops the subscription. The channel is closed by that function, so a
// reader ranging over it terminates.
//
// History is deliberately not replayed here. The WebSocket delivers what
// happens next and the HTTP endpoint delivers what happened before, which is
// the split the two endpoints already had.
func (b *Bus) Subscribe(deployID string) (<-chan Line, func()) {
	ch := make(chan Line, subscriberBuffer)

	b.mu.Lock()
	t := b.topicLocked(deployID)
	b.nextSubID++
	id := b.nextSubID
	t.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			// Removing and closing under the lock, because Publish delivers
			// under it too. Closing outside would race a send in flight.
			b.mu.Lock()
			defer b.mu.Unlock()
			if t, ok := b.topics[deployID]; ok {
				delete(t.subs, id)
			}
			close(ch)
		})
	}
}

// History returns up to limit lines recorded strictly after the since cursor,
// together with the cursor to pass on the next call. An empty since means
// "from the beginning of what is retained".
//
// An unparseable or stale cursor is treated as "from the beginning" rather
// than as an error, which is what the Redis stream range did with an id that
// had been trimmed away.
func (b *Bus) History(deployID, since string, limit int) ([]Entry, string) {
	if limit <= 0 || limit > DefaultHistory {
		limit = 200
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.topics[deployID]
	if !ok || len(t.lines) == 0 {
		return nil, ""
	}

	// The cursor names the last line already seen, so start one past it.
	start := 0
	if since != "" {
		if seq, err := strconv.Atoi(since); err == nil {
			if next := seq + 1 - t.firstSeq; next > 0 {
				start = next
			}
		}
	}
	if start >= len(t.lines) {
		return nil, since
	}

	end := start + limit
	if end > len(t.lines) {
		end = len(t.lines)
	}

	out := make([]Entry, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, Entry{
			ID:   strconv.Itoa(t.firstSeq + i),
			Line: t.lines[i],
		})
	}
	return out, out[len(out)-1].ID
}

// Tail returns the last n lines retained for a deploy. It is what gets written
// to the database when a deploy fails, so the output that explains the failure
// outlives the process that produced it.
func (b *Bus) Tail(deployID string, n int) []Line {
	if n <= 0 {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.topics[deployID]
	if !ok || len(t.lines) == 0 {
		return nil
	}
	start := len(t.lines) - n
	if start < 0 {
		start = 0
	}
	out := make([]Line, len(t.lines)-start)
	copy(out, t.lines[start:])
	return out
}

// Forget releases a deploy's retained history.
//
// It is not called when a deploy merely ends — history outlives that on
// purpose, and the topic cap is what reclaims it. This is for a deploy being
// deleted, where keeping its output would be keeping a record of something the
// operator asked to remove.
//
// It does not close subscriber channels, and that is not an oversight: a
// channel is closed by exactly one owner, the function Subscribe handed back.
// Closing here as well would be a second closer, and the race between them is
// a panic on a live request path. A subscriber simply stops receiving, which
// is what the end of a deploy means anyway.
func (b *Bus) Forget(deployID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	t, ok := b.topics[deployID]
	if !ok {
		return
	}
	if len(t.subs) == 0 {
		delete(b.topics, deployID)
		return
	}
	// Someone is still attached, so the topic has to outlive its history —
	// it owns their channels. Advance the sequence past the dropped lines so
	// a cursor issued before this call can never point inside what comes
	// after it.
	t.firstSeq += len(t.lines)
	t.lines = nil
}

// topicLocked returns the topic for a deploy, creating it if needed and
// evicting the least recently written one when the cap is exceeded. The caller
// holds b.mu.
func (b *Bus) topicLocked(deployID string) *topic {
	b.nextTopicN++
	if t, ok := b.topics[deployID]; ok {
		t.order = b.nextTopicN
		return t
	}

	if len(b.topics) >= b.maxTopics {
		var oldestID string
		var oldest uint64
		first := true
		for id, t := range b.topics {
			if first || t.order < oldest {
				oldestID, oldest, first = id, t.order, false
			}
		}
		// Only the history is reclaimed; a live subscriber on the evicted
		// topic keeps its channel and simply stops receiving, which is what
		// happens to it when the deploy ends anyway.
		if oldestID != "" {
			delete(b.topics, oldestID)
		}
	}

	t := &topic{subs: make(map[uint64]chan Line), order: b.nextTopicN}
	b.topics[deployID] = t
	return t
}

// EncodeLines renders lines for storage as one JSON array.
//
// The codec lives here so that exactly two places know the encoding — the
// adapter that archives a failed deploy's tail, and the reader that serves it
// back — and the table in between stores an opaque blob. A nil or empty slice
// encodes to nil rather than to "null", so "nothing was captured" and "the
// column is unset" are the same thing to every reader.
func EncodeLines(lines []Line) []byte {
	if len(lines) == 0 {
		return nil
	}
	data, err := json.Marshal(lines)
	if err != nil {
		// Line has no field that can fail to marshal; a failure here would be
		// a change to the struct, and losing an archived tail is not worth
		// failing the status update that carries it.
		return nil
	}
	return data
}

// DecodeLines parses what EncodeLines wrote. Malformed input yields no lines
// rather than an error: the caller is serving a failed deploy's history, and a
// corrupt archive should read as an empty one, not as a 500.
func DecodeLines(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var lines []Line
	if err := json.Unmarshal(data, &lines); err != nil {
		return nil
	}
	return lines
}
