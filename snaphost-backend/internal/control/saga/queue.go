package saga

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
)

// defaultCapacity is how many saga jobs may be waiting before Enqueue starts
// blocking. A saga job is three strings; the buffer exists so a burst of
// deploys decouples request handling from background work.
const defaultCapacity = 256

// ErrAlreadyScheduled means the same deploy already has a buffered or active
// job. It is not a processing failure: the caller's desired work is already
// represented, but reporting it lets the resume sweeper avoid claiming it
// re-enqueued work that the queue deliberately suppressed.
var ErrAlreadyScheduled = errors.New("saga already scheduled")

// Queue hands saga jobs to the worker. Durable progress and retry counts live
// in deploy_sagas; the resume sweeper reconstructs work after a restart.
type Queue struct {
	jobs      chan SagaJob
	log       *zap.Logger
	mu        sync.Mutex
	scheduled map[string]struct{}
}

// NewQueue creates a queue. A non-positive capacity takes the default.
func NewQueue(capacity int, log *zap.Logger) *Queue {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Queue{
		jobs:      make(chan SagaJob, capacity),
		log:       log,
		scheduled: make(map[string]struct{}),
	}
}

// Enqueue submits a job, blocking only if the buffer is full.
//
// It blocks rather than failing fast because callers have cancellable contexts
// and a full queue is back-pressure. The durable saga row remains available to
// the resume sweeper if a caller gives up.
func (q *Queue) Enqueue(ctx context.Context, job SagaJob) error {
	if job.EnqueuedAt.IsZero() {
		job.EnqueuedAt = time.Now().UTC()
	}

	// The resume sweeper sees durable state, not the in-memory consumer. A
	// legitimate build can run for longer than its five-minute stale cutoff,
	// so without this claim every sweep adds another copy of the active saga.
	// Keep the claim until its handler returns: it covers both buffered and
	// currently executing jobs while still allowing the next sweep to retry a
	// handler that actually failed.
	if !q.reserve(job.DeployID) {
		return ErrAlreadyScheduled
	}
	select {
	case q.jobs <- job:
		return nil
	case <-ctx.Done():
		q.release(job.DeployID)
		return ctx.Err()
	}
}

func (q *Queue) reserve(deployID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.scheduled[deployID]; exists {
		return false
	}
	q.scheduled[deployID] = struct{}{}
	return true
}

func (q *Queue) release(deployID string) {
	q.mu.Lock()
	delete(q.scheduled, deployID)
	q.mu.Unlock()
}

// Consume runs handler for each job until ctx is cancelled.
//
// A handler error is logged and dropped rather than retried here. The
// orchestrator has already recorded the failure on the saga row, and the resume
// sweeper is what brings it back — retrying in this loop as well would race the
// sweeper for the same saga and double every attempt.
func (q *Queue) Consume(ctx context.Context, handler func(SagaJob) error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-q.jobs:
			err := func() error {
				defer q.release(job.DeployID)
				return handler(job)
			}()
			if err != nil {
				q.log.Warn("saga job failed; the resume sweeper will retry it",
					zap.String("deploy_id", job.DeployID),
					zap.Error(err),
				)
			}
		}
	}
}

// Depth reports how many jobs are waiting. For metrics and tests; nothing
// decides anything on it.
func (q *Queue) Depth() int { return len(q.jobs) }
