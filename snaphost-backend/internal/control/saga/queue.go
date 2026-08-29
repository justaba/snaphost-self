package saga

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// defaultCapacity is how many saga jobs may be waiting before Enqueue starts
// blocking. A saga job is three strings; the buffer exists so a burst of
// deploys does not make the HTTP handler wait on the worker, not because
// anything is expected to queue up.
const defaultCapacity = 256

// Queue hands saga jobs to the worker.
//
// It was a Redis Stream with a consumer group, which bought at-least-once
// redelivery across processes. There is one process, and the durability it
// needed is in the database it already writes to: every job corresponds to a
// deploy_sagas row, and the resume sweeper re-enqueues any row sitting in a
// non-terminal step. A message lost to a crash is recovered from the state it
// was going to produce, rather than from a copy of the request that produced it.
//
// That is why the retry counting is gone too. The stream tracked deliveries
// through XPENDING and gave up after three; the saga row has its own
// retry_count, incremented by the orchestrator, and it is the one that survives
// a restart.
type Queue struct {
	jobs chan SagaJob
	log  *zap.Logger
}

// NewQueue creates a queue. A non-positive capacity takes the default.
func NewQueue(capacity int, log *zap.Logger) *Queue {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Queue{jobs: make(chan SagaJob, capacity), log: log}
}

// Enqueue submits a job, blocking only if the buffer is full.
//
// It blocks rather than failing fast because the callers are an HTTP handler
// with a request context and the resume sweeper with a cancellable one: both
// have a deadline of their own, and a full queue is back-pressure rather than
// an error. A caller that does give up leaves the saga row behind, so the
// sweeper picks the work up regardless.
func (q *Queue) Enqueue(ctx context.Context, job SagaJob) error {
	if job.EnqueuedAt.IsZero() {
		job.EnqueuedAt = time.Now().UTC()
	}
	select {
	case q.jobs <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
			if err := handler(job); err != nil {
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
