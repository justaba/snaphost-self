package auth

import (
	"strconv"
	"testing"
	"time"
)

func TestFailuresAccumulateUntilTheLimit(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("refused on attempt %d, before the limit was reached", i+1)
		}
		l.Fail("1.2.3.4")
	}

	ok, retryAfter := l.Allow("1.2.3.4")
	if ok {
		t.Fatal("a fourth attempt was allowed past a limit of three")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Fatalf("retryAfter = %s, want something inside the window", retryAfter)
	}
}

// Getting the password right is the strongest evidence there is that this
// client is not guessing. An operator who mistypes twice must start from zero.
func TestASuccessClearsTheAddress(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)

	l.Fail("1.2.3.4")
	l.Fail("1.2.3.4")
	l.Succeed("1.2.3.4")

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("refused on attempt %d after a success cleared the address", i+1)
		}
		l.Fail("1.2.3.4")
	}
}

// One address running out of attempts must not lock out another. The operator
// working from the office should not be refused because someone else is
// guessing from elsewhere.
func TestAddressesAreCountedSeparately(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute)

	l.Fail("1.2.3.4")
	l.Fail("1.2.3.4")

	if ok, _ := l.Allow("1.2.3.4"); ok {
		t.Fatal("the exhausted address was allowed")
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("a different address was refused")
	}
}

func TestTheWindowExpires(t *testing.T) {
	l := NewLoginLimiter(1, 10*time.Millisecond)

	l.Fail("1.2.3.4")
	if ok, _ := l.Allow("1.2.3.4"); ok {
		t.Fatal("allowed while still inside the window")
	}

	time.Sleep(20 * time.Millisecond)
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("still refused after the window passed")
	}
}

// Failures after the window has passed start a fresh window rather than adding
// to a stale count, or an address that failed once an hour for a day would be
// locked out by history.
func TestFailuresAfterTheWindowStartFresh(t *testing.T) {
	l := NewLoginLimiter(2, 10*time.Millisecond)

	l.Fail("1.2.3.4")
	l.Fail("1.2.3.4")
	time.Sleep(20 * time.Millisecond)

	l.Fail("1.2.3.4")
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("a single failure in a fresh window exhausted the limit")
	}
}

// The map must not grow without bound on a flood from rotating addresses, and
// pruning must not evict a live entry — which would let a guesser reset their
// own counter by making noise from other addresses.
func TestTheMapIsPrunedWithoutDroppingLiveEntries(t *testing.T) {
	l := NewLoginLimiter(1, 10*time.Millisecond)

	for i := 0; i < loginLimiterMaxAddresses+10; i++ {
		l.Fail("stale-" + strconv.Itoa(i))
	}
	time.Sleep(20 * time.Millisecond)

	live := NewLoginLimiter(1, time.Hour)
	live.Fail("guesser")
	for i := 0; i < loginLimiterMaxAddresses+10; i++ {
		live.Fail("noise-" + strconv.Itoa(i))
	}
	if ok, _ := live.Allow("guesser"); ok {
		t.Fatal("an exhausted address was cleared by unrelated traffic")
	}

	// The stale limiter prunes on the next failure, once its entries expired.
	l.Fail("one-more")
	l.mu.Lock()
	remaining := len(l.failures)
	l.mu.Unlock()
	if remaining > loginLimiterMaxAddresses {
		t.Fatalf("%d addresses retained, above the %d cap", remaining, loginLimiterMaxAddresses)
	}
}

func TestDefaultsAreApplied(t *testing.T) {
	l := NewLoginLimiter(0, 0)
	if l.limit != loginFailureLimit || l.window != loginFailureWindow {
		t.Fatalf("limiter = {%d, %s}, want the documented defaults", l.limit, l.window)
	}
}
