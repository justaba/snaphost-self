// Package logs provides build pipeline log publishing to Redis pub/sub channels.
package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// LogLine represents a single line of build output published to the log channel.
type LogLine struct {
	// DeployID identifies the deployment this log belongs to.
	DeployID string `json:"deploy_id"`
	// Stage is the pipeline stage producing the log (clone, detect, validate, build, scan, push).
	Stage string `json:"stage"`
	// Text is the log message content.
	Text string `json:"text"`
	// Level is the log severity (info, warn, error).
	Level string `json:"level"`
	// Timestamp is when the log line was emitted.
	Timestamp time.Time `json:"timestamp"`
}

// Publisher defines the interface for publishing build pipeline logs.
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
// the build pipeline.
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
	stream := fmt.Sprintf("logs-history:%s", deployID)
	ctx := context.Background()
	if err := p.rdb.Publish(ctx, channel, string(data)).Err(); err != nil {
		p.log.Warn("failed to publish log line",
			zap.String("deploy_id", deployID),
			zap.String("channel", channel),
			zap.Error(err),
		)
	}
	if err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: 1000,
		Approx: true,
		Values: map[string]any{"line": data},
	}).Err(); err != nil {
		// History persistence failure should not block the live channel.
		p.log.Warn("failed to append log to history stream",
			zap.String("deploy_id", deployID),
			zap.String("stream", stream),
			zap.Error(err),
		)
	}

	return nil
}

// Close is a no-op for RedisPublisher — the Redis client lifecycle is managed externally.
func (p *RedisPublisher) Close() error {
	return nil
}

// StreamingWriter wraps a Publisher to implement io.Writer. It buffers incoming
// bytes and emits one LogLine per newline-terminated line, keeping partial lines
// in the buffer until completed.
type StreamingWriter struct {
	pub      Publisher
	deployID string
	stage    string
	buf      bytes.Buffer
}

// NewStreamingWriter creates an io.Writer that emits log lines through the
// given Publisher for each newline-terminated line of output.
func NewStreamingWriter(pub Publisher, deployID, stage string) *StreamingWriter {
	return &StreamingWriter{
		pub:      pub,
		deployID: deployID,
		stage:    stage,
	}
}

// Write implements io.Writer. It splits incoming data on newlines and publishes
// complete lines immediately. Partial lines are buffered.
func (w *StreamingWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf.Write(p)

	for {
		line, err := w.buf.ReadBytes('\n')
		if err != nil {
			// No more complete lines — put the partial back.
			w.buf.Write(line)
			break
		}

		text := string(bytes.TrimRight(line, "\r\n"))
		if text == "" {
			continue
		}

		_ = w.pub.Publish(w.deployID, LogLine{
			Stage:     w.stage,
			Text:      text,
			Level:     "info",
			Timestamp: time.Now().UTC(),
		})
	}

	return n, nil
}

// Flush publishes any remaining buffered content as a final log line.
func (w *StreamingWriter) Flush() {
	if w.buf.Len() > 0 {
		_ = w.pub.Publish(w.deployID, LogLine{
			Stage:     w.stage,
			Text:      w.buf.String(),
			Level:     "info",
			Timestamp: time.Now().UTC(),
		})
		w.buf.Reset()
	}
}
