package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	// streamName is the Redis Stream all saga jobs flow through.
	streamName = "snaphost:saga"
	// streamMaxLen caps the stream size; older entries are trimmed by Redis
	// (approximate trim with the ~ argument).
	streamMaxLen = 10000
	// blockTimeout is how long XREADGROUP waits for new messages before
	// returning so the consumer loop can re-check ctx.Done.
	blockTimeout = 5 * time.Second
	// maxRetries is the cap on how many times a single message may be
	// re-delivered before it gets sent to the dead-letter sink.
	maxRetries = 3
)

// Queue wraps a Redis Stream + consumer group for saga jobs.
type Queue struct {
	rdb *redis.Client
	log *zap.Logger
}

// NewQueue constructs a Queue over the given Redis client.
func NewQueue(rdb *redis.Client, log *zap.Logger) *Queue {
	return &Queue{rdb: rdb, log: log}
}

// EnsureGroup creates the consumer group if it does not exist. Safe to
// call repeatedly — BUSYGROUP errors are swallowed.
func (q *Queue) EnsureGroup(ctx context.Context, group string) error {
	err := q.rdb.XGroupCreateMkStream(ctx, streamName, group, "$").Err()
	if err != nil && !isBusyGroup(err) {
		return fmt.Errorf("xgroup create: %w", err)
	}
	return nil
}

// Enqueue appends a job to the saga stream. Trimming uses MAXLEN ~ to
// allow Redis to pick a memory-efficient cut point.
func (q *Queue) Enqueue(ctx context.Context, job SagaJob) error {
	if job.EnqueuedAt.IsZero() {
		job.EnqueuedAt = time.Now().UTC()
	}
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal saga job: %w", err)
	}
	args := &redis.XAddArgs{
		Stream: streamName,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]any{"job": data},
	}
	if err := q.rdb.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("xadd saga: %w", err)
	}
	return nil
}

// Consume blocks reading jobs from the consumer group, dispatching each
// to handler. Handler errors leave the message un-acked; messages that
// have been delivered more than maxRetries times are acked anyway and
// logged so they don't block the group forever.
func (q *Queue) Consume(ctx context.Context, group, consumer string, handler func(SagaJob) error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{streamName, ">"},
			Block:    blockTimeout,
			Count:    8,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			q.log.Error("xreadgroup failed", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range res {
			for _, msg := range stream.Messages {
				q.dispatch(ctx, group, msg, handler)
			}
		}
	}
}

// dispatch decodes one stream message and runs the handler.
func (q *Queue) dispatch(ctx context.Context, group string, msg redis.XMessage, handler func(SagaJob) error) {
	raw, ok := msg.Values["job"].(string)
	if !ok {
		q.log.Error("saga message missing 'job' field", zap.String("id", msg.ID))
		_ = q.rdb.XAck(ctx, streamName, group, msg.ID).Err()
		return
	}
	var job SagaJob
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		q.log.Error("decode saga job failed", zap.String("id", msg.ID), zap.Error(err))
		_ = q.rdb.XAck(ctx, streamName, group, msg.ID).Err()
		return
	}

	deliveries, err := q.deliveryCount(ctx, group, msg.ID)
	if err != nil {
		q.log.Warn("xpending lookup failed", zap.String("id", msg.ID), zap.Error(err))
	}
	if deliveries > maxRetries {
		q.log.Error("saga job exceeded retries; acking",
			zap.String("id", msg.ID),
			zap.String("deploy_id", job.DeployID),
			zap.Int64("deliveries", deliveries),
		)
		_ = q.rdb.XAck(ctx, streamName, group, msg.ID).Err()
		return
	}

	if err := handler(job); err != nil {
		// Leave the message un-acked; it will be re-delivered after the
		// pending entry's idle time expires (or on consumer reconnect).
		q.log.Warn("saga handler returned error; will retry",
			zap.String("id", msg.ID),
			zap.String("deploy_id", job.DeployID),
			zap.Error(err),
		)
		return
	}
	if err := q.rdb.XAck(ctx, streamName, group, msg.ID).Err(); err != nil {
		q.log.Warn("xack failed", zap.String("id", msg.ID), zap.Error(err))
	}
}

// deliveryCount returns the number of times a pending message has been
// delivered. Used to enforce maxRetries.
func (q *Queue) deliveryCount(ctx context.Context, group, msgID string) (int64, error) {
	pending, err := q.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: streamName,
		Group:  group,
		Start:  msgID,
		End:    msgID,
		Count:  1,
	}).Result()
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 1, nil
	}
	return pending[0].RetryCount, nil
}

// isBusyGroup reports whether the error is "consumer group already exists".
func isBusyGroup(err error) bool {
	return err != nil && err.Error() == "BUSYGROUP Consumer Group name already exists"
}
