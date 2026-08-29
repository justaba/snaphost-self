// Package logs is the control plane's view of deploy log output: the interface
// the saga publishes progress through, and the reader that serves history back
// over HTTP.
//
// Both used to be Redis — a pub/sub channel logs:{deploy_id} and a capped
// stream logs-history:{deploy_id} — because the saga, the build pipeline and
// the runtime were separate processes that had to meet somewhere. They are one
// process now and meet in internal/logbus.
package logs

import "time"

// LogLine is a single structured log entry for a deploy. The build pipeline
// and the runtime have their own identical declarations, which is what four
// services publishing to one channel produced; the adapters in internal/wiring
// convert between them and the bus.
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
