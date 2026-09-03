// Package logs is orchestration's view of deploy log output: the interface
// the saga publishes progress through, and the reader that serves history back
// over HTTP.
package logs

import "time"

// LogLine is a single structured log entry for a deploy. The build pipeline
// and the runtime have component-local declarations; adapters in
// internal/wiring convert them to the common bus representation.
type LogLine struct {
	DeployID  string    `json:"deploy_id"`
	Stage     string    `json:"stage"`
	Text      string    `json:"text"`
	Level     string    `json:"level"`
	Timestamp time.Time `json:"timestamp"`
}

// Publisher publishes structured log lines for a deploy.
//
// Publish still returns an error, and no implementation returns a non-nil one
// any more. The signature is kept because every caller already treats it as
// best-effort — `_ = pub.Publish(...)` at most call sites — and narrowing it
// would be a wide diff for no behaviour change.
type Publisher interface {
	Publish(deployID string, line LogLine) error
	Close() error
}
