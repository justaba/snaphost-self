package pipeline

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// Recording the artifact enforces one invariant: either the database names the
// image, or the image is not on the host.
//
// Getting here took two wrong answers. First the call was best-effort, so the
// pipeline carried on, the scan failed, its own best-effort removal failed, and
// the image was orphaned exactly as before the call existed. Then it returned a
// Transient error on the theory that the build would be retried and the
// deterministic image name would make the retry overwrite the same tag —
// but runBuildWorker logs IsTransient and calls FinalizeAsFailed either way.
// Nothing retries. The deploy went terminal with the image on disk and
// deploys.image_ref empty, which is the leak, reached through the fix for it.

type recordingStatus struct {
	// contextErrs records the liveness of each attempt's context, which is the
	// whole point of the retry: a second attempt on the dead one is pointless.
	contextErrs []error
	failures    int
	attempts    int
}

func (s *recordingStatus) ReportBuilding(context.Context, string) error       { return nil }
func (s *recordingStatus) ReportFailed(context.Context, string, string) error { return nil }
func (s *recordingStatus) ReportImageLoaded(ctx context.Context, _, _ string) error {
	s.attempts++
	s.contextErrs = append(s.contextErrs, ctx.Err())
	if s.attempts <= s.failures {
		return errors.New("context canceled")
	}
	return nil
}

type recordingRemover struct {
	removed     []string
	contextErrs []error
	err         error
}

func (r *recordingRemover) RemoveImage(ctx context.Context, imageRef string) error {
	r.removed = append(r.removed, imageRef)
	r.contextErrs = append(r.contextErrs, ctx.Err())
	return r.err
}

func newRecordRunner(st StatusReporter, rm ImageRemover) *Runner {
	return &Runner{Status: st, Images: rm, Log: zap.NewNop()}
}

func TestRecordLoadedImageSucceedsFirstTime(t *testing.T) {
	st := &recordingStatus{}
	rm := &recordingRemover{}

	if err := newRecordRunner(st, rm).recordLoadedImage(
		context.Background(), "d1", "snaphost/proj-abc:1", zap.NewNop()); err != nil {
		t.Fatalf("recordLoadedImage: %v", err)
	}
	if st.attempts != 1 {
		t.Errorf("attempts = %d, want 1", st.attempts)
	}
	if len(rm.removed) != 0 {
		t.Errorf("a recorded image was removed: %v", rm.removed)
	}
}

// A cancelled build context is the ordinary way the first attempt fails, and
// the retry has to run on a live one or it cannot possibly succeed.
func TestRecordLoadedImageRetriesOnALiveContextAfterCancellation(t *testing.T) {
	st := &recordingStatus{failures: 1}
	rm := &recordingRemover{}

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	if err := newRecordRunner(st, rm).recordLoadedImage(
		dead, "d1", "snaphost/proj-abc:1", zap.NewNop()); err != nil {
		t.Fatalf("recordLoadedImage: %v", err)
	}
	if st.attempts != 2 {
		t.Fatalf("attempts = %d, want a retry", st.attempts)
	}
	if st.contextErrs[0] == nil {
		t.Error("the first attempt did not run on the cancelled context this test provides")
	}
	if st.contextErrs[1] != nil {
		t.Errorf("the retry ran on a context that was already %v; it could never succeed", st.contextErrs[1])
	}
	if len(rm.removed) != 0 {
		t.Errorf("an image recorded on the retry was removed anyway: %v", rm.removed)
	}
}

// The invariant. If the reference cannot be written, the artifact goes —
// because nothing is going to retry the build and overwrite it.
func TestRecordLoadedImageRemovesTheImageItCannotName(t *testing.T) {
	st := &recordingStatus{failures: 2}
	rm := &recordingRemover{}

	err := newRecordRunner(st, rm).recordLoadedImage(
		context.Background(), "d1", "snaphost/proj-abc:1", zap.NewNop())
	if err == nil {
		t.Fatal("the pipeline continued without a durable reference to the image")
	}
	if len(rm.removed) != 1 || rm.removed[0] != "snaphost/proj-abc:1" {
		t.Fatalf("removed %v, want the unreferenced image", rm.removed)
	}
	// Same reasoning as the retry: the build context may be why the write
	// failed, so the removal cannot depend on it either.
	if rm.contextErrs[0] != nil {
		t.Errorf("the removal ran on a context that was already %v", rm.contextErrs[0])
	}
}

// The removal must also survive the build context being the thing that died.
func TestRecordLoadedImageRemovesOnAFreshContextAfterCancellation(t *testing.T) {
	st := &recordingStatus{failures: 2}
	rm := &recordingRemover{}

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	if err := newRecordRunner(st, rm).recordLoadedImage(
		dead, "d1", "snaphost/proj-abc:1", zap.NewNop()); err == nil {
		t.Fatal("expected the build to fail")
	}
	if len(rm.removed) != 1 {
		t.Fatalf("removed %v, want the unreferenced image", rm.removed)
	}
	if rm.contextErrs[0] != nil {
		t.Errorf("the removal inherited the dead build context: %v", rm.contextErrs[0])
	}
}

// Both the database and the daemon are unreachable. Nothing durable can be
// written anywhere — a deferred-cleanup record would need the database that
// just refused — so the build still fails and the log names the image. This is
// the one residual leak, and it is recorded rather than pretended away.
func TestRecordLoadedImageStillFailsWhenTheImageCannotBeRemovedEither(t *testing.T) {
	st := &recordingStatus{failures: 2}
	rm := &recordingRemover{err: errors.New("daemon unreachable")}

	err := newRecordRunner(st, rm).recordLoadedImage(
		context.Background(), "d1", "snaphost/proj-abc:1", zap.NewNop())
	if err == nil {
		t.Fatal("the pipeline continued with an image it can neither name nor remove")
	}
	if len(rm.removed) != 1 {
		t.Errorf("the removal was not attempted: %v", rm.removed)
	}
}

// Permanent, not Transient. The worker logs the flag and finalises as failed
// regardless, so claiming a retry that does not exist is how the previous
// version of this function reasoned itself into leaking.
func TestRecordLoadedImageFailsPermanently(t *testing.T) {
	st := &recordingStatus{failures: 2}

	err := newRecordRunner(st, &recordingRemover{}).recordLoadedImage(
		context.Background(), "d1", "snaphost/proj-abc:1", zap.NewNop())
	if !IsPermanent(err) {
		t.Fatalf("error is %v; nothing retries a build, so this must not claim to be retryable", err)
	}
}

// A runner with no reporter is a test configuration, not a deployment.
func TestRecordLoadedImageWithoutAReporterIsANoOp(t *testing.T) {
	rm := &recordingRemover{}
	if err := newRecordRunner(nil, rm).recordLoadedImage(
		context.Background(), "d1", "snaphost/proj-abc:1", zap.NewNop()); err != nil {
		t.Fatalf("recordLoadedImage: %v", err)
	}
	if len(rm.removed) != 0 {
		t.Errorf("an image was removed with no reporter configured: %v", rm.removed)
	}
}
