package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Queue manages build jobs using Redis Streams with consumer groups for
// reliable, horizontally-scalable job processing.
type Queue struct {
	rdb *redis.Client
	log *zap.Logger
}

// NewQueue creates a new Queue backed by the provided Redis client.
func NewQueue(rdb *redis.Client, log *zap.Logger) *Queue {
	return &Queue{rdb: rdb, log: log}
}

// EnsureGroup creates the consumer group if it does not already exist.
// Safe to call multiple times — BUSYGROUP errors are silently ignored.
func (q *Queue) EnsureGroup(ctx context.Context) error {
	err := q.rdb.XGroupCreateMkStream(ctx, StreamName, ConsumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}

// Enqueue adds a build job to the Redis Stream and returns the entry ID.
func (q *Queue) Enqueue(ctx context.Context, job Job) (string, error) {
	job.QueuedAt = time.Now().UTC()

	data, err := json.Marshal(job)
	if err != nil {
		return "", fmt.Errorf("marshal job: %w", err)
	}

	id, err := q.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamName,
		Values: map[string]interface{}{"data": string(data)},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("enqueue job: %w", err)
	}

	q.log.Info("job enqueued",
		zap.String("stream_id", id),
		zap.String("deploy_id", job.DeployID),
		zap.String("user_id", job.UserID),
	)

	return id, nil
}

// Consume blocks and reads jobs from the stream via XREADGROUP. For each
// message, it calls handler. On success the message is acknowledged; on error
// the message is left pending for later retry or dead-letter processing.
// Consume returns when the context is cancelled.
func (q *Queue) Consume(ctx context.Context, consumerName string, handler func(Job) error) error {
	q.log.Info("consumer started",
		zap.String("group", ConsumerGroup),
		zap.String("consumer", consumerName),
	)

	for {
		select {
		case <-ctx.Done():
			q.log.Info("consumer shutting down", zap.String("consumer", consumerName))
			return ctx.Err()
		default:
		}

		streams, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    ConsumerGroup,
			Consumer: consumerName,
			Streams:  []string{StreamName, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()

		if err != nil {
			if err == redis.Nil {
				continue // no new messages
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			q.log.Error("xreadgroup error", zap.Error(err))
			time.Sleep(1 * time.Second)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				raw, ok := msg.Values["data"].(string)
				if !ok {
					q.log.Error("invalid message format", zap.String("stream_id", msg.ID))
					q.ack(ctx, msg.ID)
					continue
				}

				var job Job
				if err := json.Unmarshal([]byte(raw), &job); err != nil {
					q.log.Error("unmarshal job failed",
						zap.String("stream_id", msg.ID),
						zap.Error(err),
					)
					q.ack(ctx, msg.ID)
					continue
				}
				job.ID = msg.ID

				if err := handler(job); err != nil {
					q.log.Error("job handler failed",
						zap.String("stream_id", msg.ID),
						zap.String("deploy_id", job.DeployID),
						zap.Error(err),
					)
					// Ack anyway — permanent failure, do not retry indefinitely.
					// Pipeline.Run already reported the failure to StatusReporter.
					q.ack(ctx, msg.ID)
					continue
				}

				q.ack(ctx, msg.ID)
			}
		}
	}
}

// ack acknowledges a message in the consumer group.
func (q *Queue) ack(ctx context.Context, id string) {
	if err := q.rdb.XAck(ctx, StreamName, ConsumerGroup, id).Err(); err != nil {
		q.log.Error("xack failed", zap.String("stream_id", id), zap.Error(err))
	}
}

// activeKey returns the Redis sorted set key for tracking a user's in-flight builds.
func activeKey(userID string) string {
	return fmt.Sprintf("snaphost:builds:active:%s", userID)
}

// CountActive returns the number of in-flight builds for the given user.
func (q *Queue) CountActive(ctx context.Context, userID string) (int, error) {
	count, err := q.rdb.ZCard(ctx, activeKey(userID)).Result()
	if err != nil {
		return 0, fmt.Errorf("count active builds: %w", err)
	}
	return int(count), nil
}

// MarkActive records a build job as in-flight for the given user.
func (q *Queue) MarkActive(ctx context.Context, userID, jobID string) error {
	err := q.rdb.ZAdd(ctx, activeKey(userID), redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: jobID,
	}).Err()
	if err != nil {
		return fmt.Errorf("mark active: %w", err)
	}
	return nil
}

// MarkDone removes a build job from the user's in-flight set.
func (q *Queue) MarkDone(ctx context.Context, userID, jobID string) error {
	err := q.rdb.ZRem(ctx, activeKey(userID), jobID).Err()
	if err != nil {
		return fmt.Errorf("mark done: %w", err)
	}
	return nil
}
