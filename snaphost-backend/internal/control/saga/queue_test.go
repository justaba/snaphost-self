package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestSagaQueueDeliversJobs(t *testing.T) {
	q := NewQueue(0, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 1)
	go func() {
		_ = q.Consume(ctx, func(job SagaJob) error {
			seen <- job.DeployID
			return nil
		})
	}()

	if err := q.Enqueue(ctx, SagaJob{DeployID: "d1"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	select {
	case got := <-seen:
		if got != "d1" {
			t.Fatalf("got %q, want d1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the job")
	}
}

func TestEnqueueStampsTheTime(t *testing.T) {
	q := NewQueue(0, zap.NewNop())
	if err := q.Enqueue(context.Background(), SagaJob{DeployID: "d1"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job := <-q.jobs; job.EnqueuedAt.IsZero() {
		t.Error("EnqueuedAt was not set")
	}
}

// A job that arrives with its own timestamp keeps it. The resume sweeper sets
// one, and overwriting it would hide how long a saga has been going round.
func TestEnqueuePreservesAnExistingTimestamp(t *testing.T) {
	q := NewQueue(0, zap.NewNop())
	when := time.Now().Add(-time.Hour).UTC()

	if err := q.Enqueue(context.Background(), SagaJob{DeployID: "d1", EnqueuedAt: when}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job := <-q.jobs; !job.EnqueuedAt.Equal(when) {
		t.Errorf("EnqueuedAt = %s, want the caller's %s", job.EnqueuedAt, when)
	}
}

func TestEnqueueOnAFullQueueHonoursTheContext(t *testing.T) {
	q := NewQueue(1, zap.NewNop())
	ctx := context.Background()

	if err := q.Enqueue(ctx, SagaJob{DeployID: "d1"}); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}

	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := q.Enqueue(deadline, SagaJob{DeployID: "d2"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Enqueue on a full queue = %v, want a deadline error", err)
	}
}

// A handler error is the normal way a saga step reports "try again later". The
// consumer must keep going: the resume sweeper is what brings that saga back,
// and a loop that stopped here would strand every deploy behind it.
func TestAFailingHandlerDoesNotStopTheConsumer(t *testing.T) {
	q := NewQueue(0, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 2)
	go func() {
		_ = q.Consume(ctx, func(job SagaJob) error {
			seen <- job.DeployID
			return errors.New("step failed")
		})
	}()

	for _, id := range []string{"d1", "d2"} {
		if err := q.Enqueue(ctx, SagaJob{DeployID: id}); err != nil {
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
	q := NewQueue(0, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- q.Consume(ctx, func(SagaJob) error { return nil }) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Consume returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Consume did not return when its context ended")
	}
}
