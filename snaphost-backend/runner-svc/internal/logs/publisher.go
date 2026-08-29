// Package logs provides runtime log publishing to Redis pub/sub channels.
// The channel shape and LogLine format match builder-svc for UI consistency.
package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// LogLine represents a single line of runtime output published to the log channel.
type LogLine struct {
	// DeployID identifies the deployment this log belongs to.
	DeployID string `json:"deploy_id"`
	// Stage is the runtime stage: "runtime-startup", "runtime", or "runtime-shutdown".
	Stage string `json:"stage"`
	// Text is the log message content.
	Text string `json:"text"`
	// Level is the log severity (info, warn, error).
	Level string `json:"level"`
	// Timestamp is when the log line was emitted.
	Timestamp time.Time `json:"timestamp"`
}

// Publisher defines the interface for publishing runtime logs.
type Publisher interface {
	// Publish sends a log line to the channel for the given deploy.
	Publish(deployID string, line LogLine) error
	// Close releases any resources held by the publisher.
	Close() error
}

// RedisPublisher implements Publisher by publishing JSON-encoded log lines
// to Redis pub/sub channels named logs:{deploy_id}.
type RedisPublisher struct {
	rdb *redis.Client
	log *zap.Logger
}

// NewRedisPublisher creates a new RedisPublisher.
func NewRedisPublisher(rdb *redis.Client, log *zap.Logger) *RedisPublisher {
	return &RedisPublisher{rdb: rdb, log: log}
}

// Publish sends a log line to the Redis pub/sub channel for the given deploy.
// Publish errors are logged but never returned — log publishing must not block
// runtime operations.
func (p *RedisPublisher) Publish(deployID string, line LogLine) error {
	line.DeployID = deployID
	if line.Timestamp.IsZero() {
		line.Timestamp = time.Now().UTC()
	}

	data, err := json.Marshal(line)
	if err != nil {
		p.log.Warn("failed to marshal log line",
			zap.String("deploy_id", deployID),
			zap.Error(err),
		)
		return nil
	}

	channel := fmt.Sprintf("logs:%s", deployID)
	if err := p.rdb.Publish(context.Background(), channel, string(data)).Err(); err != nil {
		p.log.Warn("failed to publish log line",
			zap.String("deploy_id", deployID),
			zap.String("channel", channel),
			zap.Error(err),
		)
	}

	return nil
}

// Close is a no-op for RedisPublisher — the Redis client lifecycle is managed externally.
func (p *RedisPublisher) Close() error {
	return nil
}
