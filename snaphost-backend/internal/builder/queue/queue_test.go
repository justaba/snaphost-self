package queue

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestQueue(capacity int) *Queue { return NewQueue(capacity, zap.NewNop()) }

func TestEnqueuedJobsReachTheHandlerInOrder(t *testing.T) {
	q := newTestQueue(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 3)
	go func() {
		_ = q.Consume(ctx, func(job Job) error {
			seen <- job.DeployID
			return nil
		})
	}()

	for _, id := range []string{"a", "b", "c"} {
		if _, err := q.Enqueue(ctx, Job{DeployID: id}); err != nil {
			t.Fatalf("Enqueue(%s): %v", id, err)
		}
	}

	for _, want := range []string{"a", "b", "c"} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("got %q, want %q — jobs are out of order", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func TestEnqueueAssignsAUniqueIDAndTimestamp(t *testing.T) {
	q := newTestQueue(0)
	ctx := context.Background()

	first, err := q.Enqueue(ctx, Job{DeployID: "a"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	second, err := q.Enqueue(ctx, Job{DeployID: "b"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if first == "" || first == second {
		t.Fatalf("job ids %q and %q are not unique", first, second)
	}

	job := <-q.jobs
	if job.QueuedAt.IsZero() {
		t.Error("QueuedAt was not set")
	}
	if job.ID != first {
		t.Errorf("job carries id %q, Enqueue returned %q", job.ID, first)
	}
}

// A full queue must apply back-pressure rather than drop, and must honour the
// caller's context so an HTTP handler cannot be parked forever.
func TestEnqueueBlocksWhenFullAndHonoursTheContext(t *testing.T) {
	q := newTestQueue(1)
	ctx := context.Background()

	if _, err := q.Enqueue(ctx, Job{DeployID: "a"}); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}

	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := q.Enqueue(deadline, Job{DeployID: "b"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Enqueue on a full queue = %v, want a deadline error", err)
	}
	if q.Depth() != 1 {
		t.Fatalf("depth = %d after a refused enqueue, want 1", q.Depth())
	}
}

// A handler error must not stop the consumer. The pipeline has already
// recorded the failure against the deploy; a loop that exits here would take
// every later build down with it.
func TestAFailingHandlerDoesNotStopTheConsumer(t *testing.T) {
	q := newTestQueue(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 2)
	go func() {
		_ = q.Consume(ctx, func(job Job) error {
			seen <- job.DeployID
			return errors.New("pipeline failed")
		})
	}()

	for _, id := range []string{"a", "b"} {
		if _, err := q.Enqueue(ctx, Job{DeployID: id}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	for i := 0; i < 2; i++ {
		select {
		case <-seen:
		case <-time.After(2 * time.Second):
			t.Fatal("the consumer stopped after a handler error")
		}
	}
}

func TestConsumeReturnsWhenTheContextEnds(t *testing.T) {
	q := newTestQueue(0)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- q.Consume(ctx, func(Job) error { return nil }) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Consume returned %v on cancellation, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Consume did not return when its context ended")
	}
}

func TestInFlightAccounting(t *testing.T) {
	q := newTestQueue(0)
	ctx := context.Background()

	count := func(user string) int {
		n, err := q.CountActive(ctx, user)
		if err != nil {
			t.Fatalf("CountActive: %v", err)
		}
		return n
	}

	if count("u1") != 0 {
		t.Fatal("a user with no builds counted as active")
	}
	if err := q.MarkActive(ctx, "u1", "j1"); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	if err := q.MarkActive(ctx, "u1", "j2"); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	if err := q.MarkActive(ctx, "u2", "j3"); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	if count("u1") != 2 || count("u2") != 1 {
		t.Fatalf("counts = u1:%d u2:%d, want 2 and 1", count("u1"), count("u2"))
	}

	if err := q.MarkDone(ctx, "u1", "j1"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if count("u1") != 1 {
		t.Fatalf("u1 = %d after one MarkDone, want 1", count("u1"))
	}

	// Marking the same job done twice, and marking an unknown job done, are
	// both things the pipeline's defer can do on an error path.
	if err := q.MarkDone(ctx, "u1", "j1"); err != nil {
		t.Fatalf("repeated MarkDone: %v", err)
	}
	if err := q.MarkDone(ctx, "nobody", "j9"); err != nil {
		t.Fatalf("MarkDone for an unknown user: %v", err)
	}
}

// The Redis sorted set this replaced had no expiry on its members, so a build
// killed mid-flight left its entry behind and shrank that user's limit for
// good. Emptying the user's entry is what keeps the map from growing instead.
func TestTheInFlightMapDoesNotLeakUsers(t *testing.T) {
	q := newTestQueue(0)
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		user := "u" + strconv.Itoa(i)
		if err := q.MarkActive(ctx, user, "j"); err != nil {
			t.Fatalf("MarkActive: %v", err)
		}
		if err := q.MarkDone(ctx, user, "j"); err != nil {
			t.Fatalf("MarkDone: %v", err)
		}
	}

	q.mu.Lock()
	remaining := len(q.active)
	q.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d users left in the in-flight map after every build finished", remaining)
	}
}

func TestConcurrentEnqueueAndAccounting(t *testing.T) {
	q := newTestQueue(256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = q.Consume(ctx, func(Job) error { return nil }) }()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = q.Enqueue(ctx, Job{DeployID: strconv.Itoa(i)})
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			user := "u" + strconv.Itoa(i%3)
			for j := 0; j < 50; j++ {
				_ = q.MarkActive(ctx, user, strconv.Itoa(j))
				_, _ = q.CountActive(ctx, user)
				_ = q.MarkDone(ctx, user, strconv.Itoa(j))
			}
		}(i)
	}
	wg.Wait()
}
