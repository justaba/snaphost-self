package buildevents

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestWaitReceivesATerminalEvent(t *testing.T) {
	b := New(0)

	go func() {
		time.Sleep(10 * time.Millisecond)
		b.Publish(Event{Type: Completed, DeployID: "d1", ImageRef: "img:1", Port: 3000})
	}()

	ev, err := b.Wait(context.Background(), "d1", time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if ev.Type != Completed || ev.ImageRef != "img:1" || ev.Port != 3000 {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Timestamp.IsZero() {
		t.Error("timestamp was not filled in")
	}
}

// The reason this package retains anything. A build that finishes before the
// saga's wait step runs must be answered immediately, not waited out.
func TestWaitIsAnsweredByAnAlreadyFinishedBuild(t *testing.T) {
	b := New(0)
	b.Publish(Event{Type: Failed, DeployID: "d1", Reason: "exit status 1"})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	ev, err := b.Wait(ctx, "d1", time.Hour)
	if err != nil {
		t.Fatalf("Wait on a finished build: %v", err)
	}
	if ev.Type != Failed || ev.Reason != "exit status 1" {
		t.Fatalf("event = %+v", ev)
	}
}

// "started" is not an outcome. Returning it would make the saga treat a build
// in progress as one that ended, and move on with no image.
func TestStartedIsNotAnOutcome(t *testing.T) {
	b := New(0)

	go func() {
		b.Publish(Event{Type: Started, DeployID: "d1"})
		time.Sleep(20 * time.Millisecond)
		b.Publish(Event{Type: Completed, DeployID: "d1", ImageRef: "img:1"})
	}()

	ev, err := b.Wait(context.Background(), "d1", time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if ev.Type != Completed {
		t.Fatalf("Wait returned %q; only terminal events are outcomes", ev.Type)
	}

	// A "started" event must not be retained either, or the next waiter would
	// be answered with it instantly.
	if _, err := b.Wait(context.Background(), "d2", 30*time.Millisecond); err == nil {
		t.Fatal("Wait on an untouched deploy returned an event")
	}
}

func TestWaitTimesOut(t *testing.T) {
	b := New(0)

	start := time.Now()
	if _, err := b.Wait(context.Background(), "d1", 30*time.Millisecond); err == nil {
		t.Fatal("Wait did not time out")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Wait took %s to time out", elapsed)
	}
}

func TestWaitHonoursACancelledContext(t *testing.T) {
	b := New(0)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if _, err := b.Wait(ctx, "d1", time.Hour); err == nil {
		t.Fatal("Wait ignored its cancelled context")
	}
}

// Events must not cross deploys. Answering one build's saga with another's
// image reference would deploy the wrong image.
func TestEventsAreScopedToOneDeploy(t *testing.T) {
	b := New(0)
	b.Publish(Event{Type: Completed, DeployID: "d2", ImageRef: "wrong"})

	if _, err := b.Wait(context.Background(), "d1", 30*time.Millisecond); err == nil {
		t.Fatal("a waiter on d1 was answered by d2's event")
	}
}

func TestSeveralWaitersOnOneDeployAllGetTheEvent(t *testing.T) {
	b := New(0)

	var wg sync.WaitGroup
	results := make([]error, 3)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = b.Wait(context.Background(), "d1", 2*time.Second)
		}(i)
	}

	time.Sleep(20 * time.Millisecond)
	b.Publish(Event{Type: Completed, DeployID: "d1", ImageRef: "img:1"})
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("waiter %d: %v", i, err)
		}
	}
}

func TestRetentionIsBounded(t *testing.T) {
	b := New(2)

	for i := 0; i < 3; i++ {
		b.Publish(Event{Type: Completed, DeployID: "d" + strconv.Itoa(i)})
	}

	if _, err := b.Wait(context.Background(), "d0", 20*time.Millisecond); err == nil {
		t.Error("the oldest retained outcome was not evicted")
	}
	for _, id := range []string{"d1", "d2"} {
		if _, err := b.Wait(context.Background(), id, 20*time.Millisecond); err != nil {
			t.Errorf("outcome for %s was lost: %v", id, err)
		}
	}
}

// Re-publishing for a deploy already retained must overwrite rather than count
// as a new entry against the cap.
func TestRepublishingDoesNotConsumeRetentionSlots(t *testing.T) {
	b := New(2)

	b.Publish(Event{Type: Completed, DeployID: "d0", ImageRef: "first"})
	b.Publish(Event{Type: Completed, DeployID: "d0", ImageRef: "second"})
	b.Publish(Event{Type: Completed, DeployID: "d1"})

	ev, err := b.Wait(context.Background(), "d0", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("d0 was evicted by its own second event: %v", err)
	}
	if ev.ImageRef != "second" {
		t.Fatalf("image ref = %q, want the later event", ev.ImageRef)
	}
}

func TestForgetDropsTheRetainedOutcome(t *testing.T) {
	b := New(0)
	b.Publish(Event{Type: Completed, DeployID: "d1"})
	b.Forget("d1")

	if _, err := b.Wait(context.Background(), "d1", 20*time.Millisecond); err == nil {
		t.Fatal("Forget left the outcome behind")
	}
}

// Publishing while a waiter registers is the exact interleaving this package
// exists for. Run under -race.
func TestConcurrentPublishAndWait(t *testing.T) {
	b := New(8)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		id := "d" + strconv.Itoa(i%4)
		wg.Add(2)
		go func() {
			defer wg.Done()
			b.Publish(Event{Type: Completed, DeployID: id, ImageRef: "img"})
		}()
		go func() {
			defer wg.Done()
			_, _ = b.Wait(context.Background(), id, 50*time.Millisecond)
		}()
	}
	wg.Wait()
}
