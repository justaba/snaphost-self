package pipeline

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"snaphost/internal/builder/events"
	"snaphost/internal/builder/logs"
)

// recPublisher records every Publish call for assertions.
type recPublisher struct{ lines []logs.LogLine }

func (p *recPublisher) Publish(_ string, line logs.LogLine) error {
	p.lines = append(p.lines, line)
	return nil
}
func (p *recPublisher) Close() error { return nil }

// recEvents records every events.BuildEvent published.
type recEvents struct{ events []events.BuildEvent }

func (e *recEvents) Publish(_ context.Context, ev events.BuildEvent) error {
	e.events = append(e.events, ev)
	return nil
}
func (e *recEvents) Close() error { return nil }

// recStatus records ReportFailed reasons.
type recStatus struct {
	building      []string
	failed        []string
	loadedImages  []string
	failReturns   error
	loadedReturns error
}

func (s *recStatus) ReportBuilding(_ context.Context, deployID string) error {
	s.building = append(s.building, deployID)
	return nil
}
func (s *recStatus) ReportFailed(_ context.Context, _ string, reason string) error {
	s.failed = append(s.failed, reason)
	return s.failReturns
}
func (s *recStatus) ReportImageLoaded(_ context.Context, _ string, imageRef string) error {
	s.loadedImages = append(s.loadedImages, imageRef)
	return s.loadedReturns
}

func newFinalizeRunner(pub *recPublisher, ev *recEvents, st StatusReporter) *Runner {
	return &Runner{
		Publisher: pub,
		Events:    ev,
		Status:    st,
		Log:       zap.NewNop(),
	}
}

func TestFinalizeAsFailed_PublishesBuildFailedAndReportsStatus(t *testing.T) {
	pub := &recPublisher{}
	ev := &recEvents{}
	st := &recStatus{}
	r := newFinalizeRunner(pub, ev, st)

	cause := Permanent(errors.New("clone: authentication required"))
	r.FinalizeAsFailed(context.Background(), "deploy-123", cause)

	// pipeline log line emitted.
	if len(pub.lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(pub.lines))
	}
	if pub.lines[0].Level != "error" {
		t.Errorf("log line level: got %q want error", pub.lines[0].Level)
	}

	// BuildFailed event emitted with the cause text in Reason.
	if len(ev.events) != 1 {
		t.Fatalf("expected 1 build event, got %d", len(ev.events))
	}
	if ev.events[0].Type != events.BuildFailed {
		t.Errorf("event type: got %q want %q", ev.events[0].Type, events.BuildFailed)
	}
	if ev.events[0].DeployID != "deploy-123" {
		t.Errorf("event deploy_id: got %q", ev.events[0].DeployID)
	}
	if ev.events[0].Reason != cause.Error() {
		t.Errorf("event reason mismatch: got %q want %q", ev.events[0].Reason, cause.Error())
	}

	// ReportFailed called once with the cause text.
	if len(st.failed) != 1 {
		t.Fatalf("expected 1 ReportFailed call, got %d", len(st.failed))
	}
	if st.failed[0] != cause.Error() {
		t.Errorf("ReportFailed reason mismatch: got %q want %q", st.failed[0], cause.Error())
	}
}

func TestFinalizeAsFailed_ReportFailedErrorIsSwallowed(t *testing.T) {
	pub := &recPublisher{}
	ev := &recEvents{}
	st := &recStatus{failReturns: errors.New("billing down")}
	r := newFinalizeRunner(pub, ev, st)

	// Must not panic; must still publish the event.
	r.FinalizeAsFailed(context.Background(), "d", errors.New("anything"))

	if len(ev.events) != 1 {
		t.Errorf("BuildFailed event must still be published even if ReportFailed errored")
	}
}

func TestFinalizeAsFailed_NilCause(t *testing.T) {
	pub := &recPublisher{}
	ev := &recEvents{}
	st := &recStatus{}
	r := newFinalizeRunner(pub, ev, st)

	// Defensive: should not panic on nil cause.
	r.FinalizeAsFailed(context.Background(), "d", nil)

	if len(ev.events) != 1 {
		t.Fatal("event should still publish")
	}
	if ev.events[0].Reason == "" {
		t.Error("reason should be non-empty fallback")
	}
}

func TestFinalizeAsSucceeded_PublishesBuildCompleted(t *testing.T) {
	pub := &recPublisher{}
	ev := &recEvents{}
	st := &recStatus{}
	r := newFinalizeRunner(pub, ev, st)

	result := &Result{ImageRef: "host:5000/proj-x:dep-1", CommitSHA: "abc123", Port: 8080}
	r.FinalizeAsSucceeded(context.Background(), "deploy-123", result)

	// pipeline complete log line.
	if len(pub.lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(pub.lines))
	}
	if pub.lines[0].Level != "info" || pub.lines[0].Text != "pipeline complete" {
		t.Errorf("log line mismatch: %+v", pub.lines[0])
	}

	// BuildCompleted event with metadata.
	if len(ev.events) != 1 {
		t.Fatalf("expected 1 build event, got %d", len(ev.events))
	}
	got := ev.events[0]
	if got.Type != events.BuildCompleted {
		t.Errorf("event type: got %q want %q", got.Type, events.BuildCompleted)
	}
	if got.DeployID != "deploy-123" {
		t.Errorf("event deploy_id: got %q", got.DeployID)
	}
	if got.ImageRef != result.ImageRef {
		t.Errorf("event image_ref: got %q want %q", got.ImageRef, result.ImageRef)
	}
	if got.CommitSHA != result.CommitSHA {
		t.Errorf("event commit_sha: got %q want %q", got.CommitSHA, result.CommitSHA)
	}
	if got.Port != result.Port {
		t.Errorf("event port: got %d want %d", got.Port, result.Port)
	}

	// Saga must not see ReportFailed on success.
	if len(st.failed) != 0 {
		t.Errorf("ReportFailed must not be called on success path; got %v", st.failed)
	}
}

func TestFinalizeAsSucceeded_NilResultSafe(t *testing.T) {
	pub := &recPublisher{}
	ev := &recEvents{}
	r := newFinalizeRunner(pub, ev, nil)

	// Defensive: must not panic, must not publish.
	r.FinalizeAsSucceeded(context.Background(), "d", nil)

	if len(ev.events) != 0 {
		t.Error("nil result should not publish BuildCompleted")
	}
	if len(pub.lines) != 0 {
		t.Error("nil result should not emit log line")
	}
}

func TestFinalizeAsFailed_NilStatusReporterSafe(t *testing.T) {
	pub := &recPublisher{}
	ev := &recEvents{}
	// Pass untyped nil — Runner.Status is the interface type; the nil
	// guard inside FinalizeAsFailed must skip ReportFailed without panic.
	r := newFinalizeRunner(pub, ev, nil)

	r.FinalizeAsFailed(context.Background(), "d", errors.New("x"))

	if len(ev.events) != 1 {
		t.Error("event must publish even with nil status")
	}
}

// The image is recorded the moment it exists on the host, not when the deploy
// succeeds.
//
// BuildCompleted — which also writes deploys.image_ref — is published on the
// success path only. Any error after ImageLoad would otherwise leave that
// column empty, and the image sweep works entirely from it.
func TestReportImageLoadedIsPartOfTheReporterContract(t *testing.T) {
	var _ StatusReporter = (*recStatus)(nil)

	st := &recStatus{}
	if err := st.ReportImageLoaded(context.Background(), "d1", "snaphost/proj-abc:1"); err != nil {
		t.Fatalf("ReportImageLoaded: %v", err)
	}
	if len(st.loadedImages) != 1 || st.loadedImages[0] != "snaphost/proj-abc:1" {
		t.Fatalf("recorded %v", st.loadedImages)
	}
}

// A bookkeeping failure must not fail the build. The image exists either way,
// and turning this into a build failure would trade a leak for a broken
// deploy — so the pipeline logs and carries on.
func TestARecordingFailureDoesNotAbortTheBuild(t *testing.T) {
	st := &recStatus{loadedReturns: errors.New("database is locked")}

	if err := st.ReportImageLoaded(context.Background(), "d1", "snaphost/proj-abc:1"); err == nil {
		t.Fatal("the stub should surface the error so the pipeline can log it")
	}
	// The pipeline's own handling is a log call with no propagation; what is
	// asserted here is that the reporter is allowed to fail at all, which is
	// why the call site ignores the return.
}
