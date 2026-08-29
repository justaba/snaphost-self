package logbus

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func line(text string) Line { return Line{Stage: "build", Level: "info", Text: text} }

func TestSubscriberReceivesWhatIsPublishedNext(t *testing.T) {
	b := New(0, 0)

	ch, stop := b.Subscribe("d1")
	defer stop()

	b.Publish("d1", line("one"))
	b.Publish("d1", line("two"))

	for _, want := range []string{"one", "two"} {
		select {
		case got := <-ch:
			if got.Text != want {
				t.Fatalf("got %q, want %q", got.Text, want)
			}
			if got.DeployID != "d1" {
				t.Errorf("deploy id = %q, want d1", got.DeployID)
			}
			if got.Timestamp.IsZero() {
				t.Error("timestamp was not filled in")
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

// Lines for one deploy must not reach a subscriber on another. With Redis this
// was the channel name doing the work; here it is a map key, and getting it
// wrong would show one tenant another's build output.
func TestSubscriptionsAreScopedToOneDeploy(t *testing.T) {
	b := New(0, 0)

	ch, stop := b.Subscribe("d1")
	defer stop()

	b.Publish("d2", line("not yours"))
	b.Publish("d1", line("yours"))

	select {
	case got := <-ch:
		if got.Text != "yours" {
			t.Fatalf("received %q from another deploy's channel", got.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

// The whole reason publishing does not block. A subscriber that stops reading
// must cost only its own lines, because the publisher is the build pipeline.
func TestASlowSubscriberDoesNotBlockPublishing(t *testing.T) {
	b := New(0, 0)

	_, stop := b.Subscribe("d1")
	defer stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subscriberBuffer*3; i++ {
			b.Publish("d1", line("line "+strconv.Itoa(i)))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked on a subscriber that never read")
	}
}

func TestUnsubscribeStopsDeliveryAndClosesTheChannel(t *testing.T) {
	b := New(0, 0)

	ch, stop := b.Subscribe("d1")
	stop()

	b.Publish("d1", line("after unsubscribe"))

	if _, open := <-ch; open {
		t.Fatal("channel delivered a line after unsubscribe")
	}
}

// Calling the unsubscribe function twice must not panic on a closed channel.
// A WebSocket handler defers it and may also call it on an error path.
func TestUnsubscribeIsIdempotent(t *testing.T) {
	b := New(0, 0)

	_, stop := b.Subscribe("d1")
	stop()
	stop()
}

func TestHistoryPagesWithItsCursor(t *testing.T) {
	b := New(0, 0)
	for i := 0; i < 5; i++ {
		b.Publish("d1", line("line "+strconv.Itoa(i)))
	}

	first, cursor := b.History("d1", "", 2)
	if len(first) != 2 || first[0].Line.Text != "line 0" || first[1].Line.Text != "line 1" {
		t.Fatalf("first page = %+v", first)
	}

	second, cursor := b.History("d1", cursor, 2)
	if len(second) != 2 || second[0].Line.Text != "line 2" {
		t.Fatalf("second page = %+v", second)
	}

	third, _ := b.History("d1", cursor, 2)
	if len(third) != 1 || third[0].Line.Text != "line 4" {
		t.Fatalf("third page = %+v", third)
	}
}

// Reading with the cursor from the last page must return nothing rather than
// repeating the final line — the endpoint is polled with it.
func TestHistoryAtTheEndReturnsNothing(t *testing.T) {
	b := New(0, 0)
	b.Publish("d1", line("only"))

	_, cursor := b.History("d1", "", 10)
	again, _ := b.History("d1", cursor, 10)
	if len(again) != 0 {
		t.Fatalf("re-read with the end cursor returned %d entries", len(again))
	}
}

// Cursors must stay meaningful after eviction. If a sequence number were the
// index into the retained slice, trimming would silently shift every cursor
// and a poller would re-read lines it had already seen.
func TestCursorsSurviveEviction(t *testing.T) {
	b := New(3, 0)

	for i := 0; i < 3; i++ {
		b.Publish("d1", line("line "+strconv.Itoa(i)))
	}
	page, cursor := b.History("d1", "", 10)
	if len(page) != 3 {
		t.Fatalf("expected 3 retained lines, got %d", len(page))
	}

	// Two more lines push the first two out of the window.
	b.Publish("d1", line("line 3"))
	b.Publish("d1", line("line 4"))

	next, _ := b.History("d1", cursor, 10)
	if len(next) != 2 || next[0].Line.Text != "line 3" || next[1].Line.Text != "line 4" {
		t.Fatalf("after eviction, next page = %+v", next)
	}
}

func TestHistoryIsBoundedByMaxLines(t *testing.T) {
	b := New(10, 0)
	for i := 0; i < 100; i++ {
		b.Publish("d1", line("line "+strconv.Itoa(i)))
	}

	page, _ := b.History("d1", "", DefaultHistory)
	if len(page) != 10 {
		t.Fatalf("retained %d lines, want the 10-line bound", len(page))
	}
	if page[0].Line.Text != "line 90" {
		t.Fatalf("oldest retained line is %q, want line 90", page[0].Line.Text)
	}
}

func TestTailReturnsTheLastLines(t *testing.T) {
	b := New(0, 0)
	for i := 0; i < 10; i++ {
		b.Publish("d1", line("line "+strconv.Itoa(i)))
	}

	tail := b.Tail("d1", 3)
	if len(tail) != 3 || tail[0].Text != "line 7" || tail[2].Text != "line 9" {
		t.Fatalf("tail = %+v", tail)
	}
	if all := b.Tail("d1", 100); len(all) != 10 {
		t.Fatalf("asking for more than exists returned %d lines, want 10", len(all))
	}
	if none := b.Tail("unknown", 3); none != nil {
		t.Fatalf("tail of an unknown deploy = %+v, want nil", none)
	}
}

func TestForgetReleasesHistory(t *testing.T) {
	b := New(0, 0)
	b.Publish("d1", line("one"))
	b.Forget("d1")

	if page, _ := b.History("d1", "", 10); len(page) != 0 {
		t.Fatalf("history survived Forget: %+v", page)
	}
}

// Forget must not close a live subscriber's channel: the unsubscribe function
// is the only closer, and a second one racing it is a panic on a request path.
func TestForgetDoesNotCloseALiveSubscription(t *testing.T) {
	b := New(0, 0)

	ch, stop := b.Subscribe("d1")
	b.Publish("d1", line("before"))
	<-ch

	b.Forget("d1")

	select {
	case _, open := <-ch:
		if !open {
			t.Fatal("Forget closed a subscriber channel that it does not own")
		}
	default:
	}

	stop() // the owner closes, and must not panic
	if _, open := <-ch; open {
		t.Fatal("channel still open after its own unsubscribe")
	}
}

// A cursor handed out before Forget must never address a line published after
// it, or a poller would silently skip the beginning of the next run.
func TestForgetAdvancesTheSequencePastDroppedLines(t *testing.T) {
	b := New(0, 0)

	_, stop := b.Subscribe("d1")
	defer stop()

	b.Publish("d1", line("old"))
	_, cursor := b.History("d1", "", 10)

	b.Forget("d1")
	b.Publish("d1", line("new"))

	page, _ := b.History("d1", cursor, 10)
	if len(page) != 1 || page[0].Line.Text != "new" {
		t.Fatalf("page after Forget = %+v, want just the new line", page)
	}
}

// The topic cap is the backstop for a Forget that never comes. Without it a
// process that runs for months accumulates one history per deploy it ever saw.
func TestTopicsAreCapped(t *testing.T) {
	b := New(0, 2)

	b.Publish("d1", line("one"))
	b.Publish("d2", line("two"))
	b.Publish("d3", line("three"))

	if page, _ := b.History("d1", "", 10); len(page) != 0 {
		t.Error("the least recently written topic was not evicted")
	}
	for _, id := range []string{"d2", "d3"} {
		if page, _ := b.History(id, "", 10); len(page) != 1 {
			t.Errorf("topic %s lost its history", id)
		}
	}
}

// The first concurrent run of this package found a real defect: delivery
// copied the subscriber channels out from under the lock and sent afterwards,
// so an unsubscribe landing in that window closed a channel a publisher was
// about to send on. That is a panic, not a dropped line, and the way to hit it
// is a browser tab closing during a build.
func TestUnsubscribingWhilePublishingDoesNotPanic(t *testing.T) {
	b := New(0, 0)

	var wg sync.WaitGroup
	stopPublishing := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopPublishing:
				return
			default:
				b.Publish("d1", line("noise"))
			}
		}
	}()

	for i := 0; i < 200; i++ {
		_, stop := b.Subscribe("d1")
		stop()
	}
	close(stopPublishing)
	wg.Wait()
}

// The bus is written to by the build pipeline and read by request handlers at
// the same time. Run this one under -race.
func TestConcurrentPublishSubscribeAndRead(t *testing.T) {
	b := New(50, 8)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := "d" + strconv.Itoa(w%2)
			for i := 0; i < 200; i++ {
				b.Publish(id, line("line "+strconv.Itoa(i)))
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			id := "d" + strconv.Itoa(r%2)
			ch, stop := b.Subscribe(id)
			defer stop()
			go func() {
				for range ch {
				}
			}()
			for i := 0; i < 200; i++ {
				b.History(id, "", 10)
				b.Tail(id, 5)
			}
		}(r)
	}
	wg.Wait()
}
