// Package logs carries runtime container output. The Publisher interface is
// satisfied by an adapter over internal/logbus; it used to be a Redis pub/sub
// client, because the runtime was its own process.
// The channel shape and LogLine format match builder-svc for UI consistency.
package logs

import "time"

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
