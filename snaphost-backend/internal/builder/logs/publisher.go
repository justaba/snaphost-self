// Package logs carries build pipeline output. The Publisher interface is
// satisfied by an adapter over internal/logbus; it used to be a Redis pub/sub
// client, because the builder was its own process and the browser reading its
// output was on the far side of a broker.
package logs

import (
	"bytes"
	"time"
)

// LogLine represents a single line of build output published to the log channel.
type LogLine struct {
	// DeployID identifies the deployment this log belongs to.
	DeployID string `json:"deploy_id"`
	// Stage is the pipeline stage producing the log (clone, detect, validate, build).
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
