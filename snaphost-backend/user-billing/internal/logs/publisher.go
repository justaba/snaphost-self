// Package logs provides a Redis pub/sub log publisher for streaming saga
// progress to the frontend on the same channel scheme builder-svc uses
// (logs:{deploy_id}). Frontend log subscribers receive saga messages and
// build messages on the same stream.
package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// LogLine is a single structured log entry published to logs:{deploy_id}.
// Mirrors the shape used by builder-svc so frontend subscribers do not
// need a second deserializer.
type LogLine struct {
	DeployID  string    `json:"deploy_id"`
	Stage     string    `json:"stage"`
	Text      string    `json:"text"`
	Level     string    `json:"level"`
	Timestamp time.Time `json:"timestamp"`
}

// Publisher publishes structured log lines for a deploy to a transport
// (Redis pub/sub in production).
type Publisher interface {
	// Publish sends one log line on the channel for the given deploy.
	// Errors are returned but most callers treat publishing as best-effort.
	Publish(deployID string, line LogLine) error
	// Close releases any underlying resources held by the publisher.
	Close() error
}

// RedisPublisher publishes log lines to Redis pub/sub channels named
// logs:{deploy_id}.
type RedisPublisher struct {
	rdb *redis.Client
	log *zap.Logger
}

// NewRedisPublisher constructs a publisher backed by the given Redis client.
func NewRedisPublisher(rdb *redis.Client, log *zap.Logger) *RedisPublisher {
	return &RedisPublisher{rdb: rdb, log: log}
}

// Publish encodes the line as JSON and writes it both to the live pub/sub
// channel logs:{deploy_id} (for active subscribers) and to the bounded
// Redis Stream logs-history:{deploy_id} (for HTTP-based history fetches).
// The deploy_id field on the line is overwritten to match the channel.
func (p *RedisPublisher) Publish(deployID string, line LogLine) error {
	line.DeployID = deployID
	if line.Timestamp.IsZero() {
		line.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal log line: %w", err)
	}
	channel := "logs:" + deployID
	stream := "logs-history:" + deployID
	ctx := context.Background()
	if err := p.rdb.Publish(ctx, channel, data).Err(); err != nil {
		return fmt.Errorf("publish to %s: %w", channel, err)
	}
	if err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: 1000,
		Approx: true,
		Values: map[string]any{"line": data},
	}).Err(); err != nil {
		// History persistence failure should not block the live channel.
		p.log.Warn("xadd log history failed", zap.String("deploy_id", deployID), zap.Error(err))
	}
	return nil
}

// Close is a no-op — the underlying Redis client is owned by main.
func (p *RedisPublisher) Close() error { return nil }
