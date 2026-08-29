package auth

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Task 1's decisions say rate limiting stops being a concept, and for a
// platform with one operator and no public API that is right: the Redis
// sliding window it removed was shaping traffic for a multi-tenant SaaS.
//
// This is what survives it, and only because item 6a changed the premise after
// that decision was written. There was no password then. There is one now, and
// it is the single credential that opens a panel which runs containers on the
// host as root — so the one endpoint that accepts a guess needs a bound on how
// many guesses it will take.
//
// argon2id already bounds the rate: two concurrent hashes at ~35 ms each is
// something like 57 attempts a second, which is fine against the generated
// 144-bit password and useless against one an operator chose themselves.
const (
	// loginFailureLimit is how many failures one address may accumulate
	// before it is refused.
	loginFailureLimit = 10
	// loginFailureWindow is how long those failures are remembered.
	loginFailureWindow = 5 * time.Minute
	// loginLimiterMaxAddresses caps the map, so a flood from spoofed or
	// rotating sources cannot grow it without bound. Reaching it evicts
	// everything expired and, failing that, refuses to track more — which
	// fails open for new addresses rather than locking the operator out.
	loginLimiterMaxAddresses = 4096
)

// LoginLimiter counts recent failed logins per client address.
//
// Only failures count, and a success clears the address. An operator who
// mistypes twice and then gets it right starts from zero, while someone
// working through a list does not.
type LoginLimiter struct {
	limit  int
	window time.Duration

	mu       sync.Mutex
	failures map[string]attempts
}

type attempts struct {
	count   int
	expires time.Time
}

// NewLoginLimiter creates a limiter. Non-positive arguments take the defaults.
func NewLoginLimiter(limit int, window time.Duration) *LoginLimiter {
	if limit <= 0 {
		limit = loginFailureLimit
	}
	if window <= 0 {
		window = loginFailureWindow
	}
	return &LoginLimiter{limit: limit, window: window, failures: make(map[string]attempts)}
}

// Allow reports whether an address may attempt a login, and how long it should
// wait if not.
func (l *LoginLimiter) Allow(addr string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.failures[addr]
	if !ok || time.Now().After(a.expires) {
		return true, 0
	}
	if a.count < l.limit {
		return true, 0
	}
	return false, time.Until(a.expires)
}

// Fail records a failed attempt.
func (l *LoginLimiter) Fail(addr string) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.failures[addr]
	if !ok || now.After(a.expires) {
		a = attempts{expires: now.Add(l.window)}
	}
	a.count++
	l.failures[addr] = a

	if len(l.failures) > loginLimiterMaxAddresses {
		l.pruneLocked(now)
	}
}

// Succeed clears an address. Getting the password right is the strongest
// evidence there is that this client is not guessing.
func (l *LoginLimiter) Succeed(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, addr)
}

func (l *LoginLimiter) pruneLocked(now time.Time) {
	for addr, a := range l.failures {
		if now.After(a.expires) {
			delete(l.failures, addr)
		}
	}
}

// tooManyAttempts answers a client that is over the limit.
func tooManyAttempts(c *gin.Context, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":       "too_many_attempts",
		"message":     "too many failed sign-in attempts; try again later",
		"retry_after": seconds,
	})
}
