// Package events publishes coarse-grained build lifecycle events
// (started/completed/failed) to Redis pub/sub on channels named
// build-events:{deploy_id}. Subscribers (notably user-billing's saga
// orchestrator) use these to advance their state machines.
//
// This is intentionally separate from logs.Publisher: log lines are a
// stream of human-readable text, while events are structured signals.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// BuildEventType identifies which lifecycle transition an event represents.
type BuildEventType string

const (
	// BuildStarted is emitted when the pipeline transitions to "building".
	BuildStarted BuildEventType = "started"
	// BuildCompleted is emitted after a successful image push.
	BuildCompleted BuildEventType = "completed"
	// BuildFailed is emitted when the pipeline returns an error.
	BuildFailed BuildEventType = "failed"
)

// BuildEvent is the JSON body sent on build-events:{deploy_id}.
type BuildEvent struct {
	Type     BuildEventType `json:"type"`
	DeployID string         `json:"deploy_id"`
	ImageRef string         `json:"image_ref,omitempty"`
	// Port is the application listen port resolved from EXPOSE in the
	// Dockerfile, or a language-based heuristic when EXPOSE is absent.
	// 0 means the publisher could not determine a port — the consumer
	// should fall back to its configured default.
	Port      int       `json:"port,omitempty"`
	CommitSHA string    `json:"commit_sha,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Publisher publishes build lifecycle events.
type Publisher interface {
	// Publish sends one event. Best-effort: callers should log and proceed
	// rather than failing the build because the publish failed.
	Publish(ctx context.Context, event BuildEvent) error
}

// RedisEventPublisher publishes events to Redis pub/sub channels named
// build-events:{deploy_id}.
type RedisEventPublisher struct {
	rdb *redis.Client
	log *zap.Logger
}

// NewRedisEventPublisher constructs a publisher backed by the given Redis client.
func NewRedisEventPublisher(rdb *redis.Client, log *zap.Logger) *RedisEventPublisher {
	return &RedisEventPublisher{rdb: rdb, log: log}
}

// Publish marshals the event to JSON and PUBLISHes to build-events:{deploy_id}.
func (p *RedisEventPublisher) Publish(ctx context.Context, event BuildEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal build event: %w", err)
	}
	channel := "build-events:" + event.DeployID
	if err := p.rdb.Publish(ctx, channel, data).Err(); err != nil {
		return fmt.Errorf("publish to %s: %w", channel, err)
	}
	return nil
}
