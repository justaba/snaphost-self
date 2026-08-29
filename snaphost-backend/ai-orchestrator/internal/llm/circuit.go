package llm

import (
	"context"
	"sync"
	"time"
)

type circuitState int

const (
	stateClosed circuitState = iota
	stateOpen
	stateHalfOpen
)

// CircuitClient wraps an Client with a simple three-state circuit
// breaker: closed → open (after N consecutive failures) → half-open (after
// cooldown) → closed (on success) / open (on failure).
type CircuitClient struct {
	inner               *Client
	mu                  sync.Mutex
	state               circuitState
	consecutiveFailures int
	openedAt            time.Time
	failureThreshold    int
	cooldown            time.Duration
}

// NewCircuitClient wraps inner with a circuit breaker.
// failureThreshold consecutive errors open the circuit; cooldown is how long
// it stays open before a single trial request is allowed through.
func NewCircuitClient(inner *Client, failureThreshold int, cooldown time.Duration) *CircuitClient {
	return &CircuitClient{
		inner:            inner,
		state:            stateClosed,
		failureThreshold: failureThreshold,
		cooldown:         cooldown,
	}
}

// Generate forwards the request if the circuit is closed or half-open.
// Returns ErrCircuitOpen immediately when the circuit is open.
func (c *CircuitClient) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	if !c.allow() {
		return nil, ErrCircuitOpen
	}

	resp, err := c.inner.Generate(ctx, req)
	c.record(err == nil)
	return resp, err
}

// Name delegates to the inner client's identifier.
func (c *CircuitClient) Name() string { return c.inner.Name() }

// allow checks whether a request should be let through and transitions
// stateOpen → stateHalfOpen after the cooldown elapses.
func (c *CircuitClient) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case stateClosed, stateHalfOpen:
		return true
	case stateOpen:
		if time.Since(c.openedAt) >= c.cooldown {
			c.state = stateHalfOpen
			return true
		}
		return false
	}
	return true
}

// record updates circuit state based on whether the last call succeeded.
func (c *CircuitClient) record(success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if success {
		c.state = stateClosed
		c.consecutiveFailures = 0
		return
	}

	// On failure: half-open immediately returns to open for another cooldown period.
	if c.state == stateHalfOpen {
		c.state = stateOpen
		c.openedAt = time.Now()
		return
	}

	c.consecutiveFailures++
	if c.consecutiveFailures >= c.failureThreshold {
		c.state = stateOpen
		c.openedAt = time.Now()
	}
}
