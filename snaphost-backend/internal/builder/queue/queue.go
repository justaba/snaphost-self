// Package queue hands build jobs to the worker and tracks how many builds each
// user has in flight.
//
// # Durability
//
// Queue entries are process-local on purpose. The saga owns durable progress,
// and an interrupted build is rewound to enqueue fresh work rather than replay
// a request whose short-lived archive or credential may already be consumed.
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
// the scheduler off the worker's back rather than to absorb a backlog.
const defaultCapacity = 64

// Queue carries build jobs from the scheduler to the worker.
type Queue struct {
	jobs chan Job
	log  *zap.Logger

	// mu guards process-local in-flight accounting.
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
// The id identifies the job in logs and in-flight accounting. Deployment state
// remains keyed by deploy id.
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
// A handler error is logged and the job is dropped because the pipeline has
// already finalised the deploy failure. Orchestration retries are handled by
// the saga rather than by replaying a failed source build here.
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
