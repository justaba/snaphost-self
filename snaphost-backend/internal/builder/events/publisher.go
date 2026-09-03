// Package events publishes coarse-grained build lifecycle events
// (started/completed/failed). The saga orchestrator waits on them to advance
// its state machine; the Publisher interface is satisfied by the in-process
// event bus adapter.
//
// This is intentionally separate from logs.Publisher: log lines are a
// stream of human-readable text, while events are structured signals.
package events

import (
	"context"
	"time"
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

// BuildEvent describes one build lifecycle transition.
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
