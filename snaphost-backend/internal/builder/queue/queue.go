// Package queue hands build jobs to the worker and tracks how many builds each
// user has in flight.
//
// It was a Redis Stream with a consumer group, which existed so the build API
// and the build worker could be separate processes and scaled apart. They are
// one process, so the stream was a broker between a handler and a goroutine.
//
// # Durability
//
// There is none here, on purpose. The stream's un-acked message would have been
// redelivered after a crash, and that redelivery was already broken for two of
// the three source types: an uploaded archive and a git credential are both
// deleted the moment the pipeline consumes them, so re-running the job could
// only fail. The recovery that works is one step up — the saga owns a durable
// row, and a saga interrupted mid-build is rewound to enqueue a fresh build
// rather than replay a stale request.
package queue

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// defaultCapacity bounds how many build jobs may wait before Enqueue blocks.
// Builds run one at a time on this box by design, so the buffer exists to keep
// the HTTP handler off the worker's back rather than to absorb a backlog.
const defaultCapacity = 64

// Queue carries build jobs from the API to the worker.
type Queue struct {
	jobs chan Job
	log  *zap.Logger

	// mu guards the in-flight accounting. It used to be a Redis sorted set per
	// user, which had no expiry on its members: a build killed mid-flight left
	// its entry behind for good, and the user's concurrency limit shrank by one
	// permanently. A map in the process that runs the builds cannot outlive
	// them.
	mu     sync.Mutex
	active map[string]map[string]struct{}
}

// NewQueue creates a queue. A non-positive capacity takes the default.
func NewQueue(capacity int, log *zap.Logger) *Queue {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Queue{
		jobs:   make(chan Job, capacity),
		log:    log,
		active: make(map[string]map[string]struct{}),
	}
}

// Enqueue submits a build job and returns its id.
//
// The id was a Redis stream entry id and is a UUID now. Nothing parses it: it
// identifies the job in logs and in the in-flight set, and the deploy id is
// what everything else keys on.
func (q *Queue) Enqueue(ctx context.Context, job Job) (string, error) {
	job.ID = uuid.NewString()
	job.QueuedAt = time.Now().UTC()

	select {
	case q.jobs <- job:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	q.log.Info("job enqueued",
		zap.String("job_id", job.ID),
		zap.String("deploy_id", job.DeployID),
		zap.String("user_id", job.UserID),
	)
	return job.ID, nil
}

// Consume runs handler for each job until ctx is cancelled.
//
// A handler error is logged and the job is dropped. That is what the Redis
// version did too, for a documented reason: the pipeline has already reported
// the failure to the deploy, and retrying a build that failed on its own source
// would fail the same way. A retry budget is still unwritten.
func (q *Queue) Consume(ctx context.Context, handler func(Job) error) error {
	q.log.Info("build consumer started")
	for {
		select {
		case <-ctx.Done():
			q.log.Info("build consumer shutting down")
			return nil
		case job := <-q.jobs:
			if err := handler(job); err != nil {
				q.log.Error("build job failed",
					zap.String("job_id", job.ID),
					zap.String("deploy_id", job.DeployID),
					zap.Error(err),
				)
			}
		}
	}
}

// CountActive returns how many builds the user has in flight.
func (q *Queue) CountActive(_ context.Context, userID string) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.active[userID]), nil
}

// MarkActive records a build as in flight.
func (q *Queue) MarkActive(_ context.Context, userID, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active[userID] == nil {
		q.active[userID] = make(map[string]struct{})
	}
	q.active[userID][jobID] = struct{}{}
	return nil
}

// MarkDone removes a build from the in-flight set. Removing the user's entry
// when it empties keeps an operator who has ever deployed from costing a map
// entry for the life of the process.
func (q *Queue) MarkDone(_ context.Context, userID, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs := q.active[userID]
	if jobs == nil {
		return nil
	}
	delete(jobs, jobID)
	if len(jobs) == 0 {
		delete(q.active, userID)
	}
	return nil
}

// Depth reports how many jobs are waiting. For metrics and tests.
func (q *Queue) Depth() int { return len(q.jobs) }
