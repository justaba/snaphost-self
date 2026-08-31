package watchdog

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"snaphost/internal/runtime/billing"
	"snaphost/internal/runtime/config"
)

type fakeLister struct {
	expired    []billing.ExpiredDeploy
	expiredErr error
	images     []billing.ImageCleanup
	imagesErr  error
	reclaimed  int
	reclaimErr error

	expiredCalls int
	imageCalls   int
	reclaimCalls int
}

func (f *fakeLister) ReclaimStoppedDeploys(context.Context, int) (int, error) {
	f.reclaimCalls++
	return f.reclaimed, f.reclaimErr
}

func (f *fakeLister) ListExpiredDeploys(context.Context, int) ([]billing.ExpiredDeploy, error) {
	f.expiredCalls++
	return f.expired, f.expiredErr
}

func (f *fakeLister) ListImagesPendingCleanup(context.Context, int) ([]billing.ImageCleanup, error) {
	f.imageCalls++
	return f.images, f.imagesErr
}

type fakeCleaner struct {
	stopped []string
	removed []string

	stopErr   error
	removeErr func(imageRef string) error
}

func (f *fakeCleaner) StopExpired(_ context.Context, deployID, _ string) error {
	f.stopped = append(f.stopped, deployID)
	return f.stopErr
}

func (f *fakeCleaner) RemoveImage(_ context.Context, _, imageRef string) error {
	f.removed = append(f.removed, imageRef)
	if f.removeErr != nil {
		return f.removeErr(imageRef)
	}
	return nil
}

func newWatchdog(lister *fakeLister, cleaner *fakeCleaner) *Watchdog {
	return NewWatchdog(cleaner, lister, &config.Config{WatchdogIntervalSec: 60}, zap.NewNop())
}

func TestSweepStopsExpiredAndReleasesImages(t *testing.T) {
	lister := &fakeLister{
		expired: []billing.ExpiredDeploy{{ID: "deploy-1", ContainerID: "container-1"}},
		images:  []billing.ImageCleanup{{ID: "deploy-9", ImageRef: "snaphost/proj-abc:9"}},
	}
	cleaner := &fakeCleaner{}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if len(cleaner.stopped) != 1 || cleaner.stopped[0] != "deploy-1" {
		t.Errorf("stopped %v, want the expired deploy", cleaner.stopped)
	}
	if len(cleaner.removed) != 1 || cleaner.removed[0] != "snaphost/proj-abc:9" {
		t.Errorf("removed %v, want the queued image", cleaner.removed)
	}
}

// The two halves fix different problems and must not be coupled. A reclaim
// statement in this repository once failed on every tick for weeks; if that
// takes image cleanup with it, the disk fills for the same invisible reason.
func TestImageSweepRunsWhenTheExpiryQueryFails(t *testing.T) {
	lister := &fakeLister{
		expiredErr: errors.New("missing argument with index 3"),
		images:     []billing.ImageCleanup{{ID: "deploy-9", ImageRef: "snaphost/proj-abc:9"}},
	}
	cleaner := &fakeCleaner{}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if len(cleaner.removed) != 1 {
		t.Fatalf("image cleanup was skipped because the expiry query failed: removed %v", cleaner.removed)
	}
}

// Symmetrically: a broken image query must not stop deploys running past their
// TTL, which is the sweep that keeps the host's memory available.
func TestExpirySweepRunsWhenTheImageQueryFails(t *testing.T) {
	lister := &fakeLister{
		expired:   []billing.ExpiredDeploy{{ID: "deploy-1", ContainerID: "container-1"}},
		imagesErr: errors.New("no such column: image_deleted_at"),
	}
	cleaner := &fakeCleaner{}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if len(cleaner.stopped) != 1 {
		t.Fatalf("expiry sweep was skipped: stopped %v", cleaner.stopped)
	}
}

// One unremovable image must not block the rest of the queue. Nothing marks
// the failed row, so the next tick asks for it again.
func TestImageSweepContinuesPastOneFailure(t *testing.T) {
	lister := &fakeLister{
		images: []billing.ImageCleanup{
			{ID: "deploy-1", ImageRef: "snaphost/proj-abc:1"},
			{ID: "deploy-2", ImageRef: "snaphost/proj-abc:2"},
			{ID: "deploy-3", ImageRef: "snaphost/proj-abc:3"},
		},
	}
	cleaner := &fakeCleaner{
		removeErr: func(imageRef string) error {
			if imageRef == "snaphost/proj-abc:2" {
				return errors.New("image is referenced by a running container")
			}
			return nil
		},
	}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if len(cleaner.removed) != 3 {
		t.Fatalf("attempted %v, want every queued image tried", cleaner.removed)
	}
}

// A failure to stop an expired deploy must not skip the ones behind it either.
func TestExpirySweepContinuesPastOneFailure(t *testing.T) {
	lister := &fakeLister{
		expired: []billing.ExpiredDeploy{
			{ID: "deploy-1", ContainerID: "container-1"},
			{ID: "deploy-2", ContainerID: "container-2"},
		},
	}
	cleaner := &fakeCleaner{stopErr: errors.New("daemon unreachable")}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if len(cleaner.stopped) != 2 {
		t.Fatalf("attempted %v, want both expired deploys tried", cleaner.stopped)
	}
}

func TestSweepWithNothingToDoTouchesNeitherBackend(t *testing.T) {
	lister := &fakeLister{}
	cleaner := &fakeCleaner{}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if len(cleaner.stopped) != 0 || len(cleaner.removed) != 0 {
		t.Errorf("an empty sweep called the runtime: stopped %v, removed %v", cleaner.stopped, cleaner.removed)
	}
	if lister.expiredCalls != 1 || lister.imageCalls != 1 || lister.reclaimCalls != 1 {
		t.Errorf("queries run %d/%d/%d times, want one each per tick",
			lister.expiredCalls, lister.reclaimCalls, lister.imageCalls)
	}
}

// The three sweeps form a pipeline: expiry stops a deploy, reclamation gives
// up on a long-stopped one, the image sweep releases the disk. Without the
// middle one nothing ever leaves 'stopped' on its own — MarkDeleted has a
// single caller, the delete button — so every expired preview keeps its image
// for the life of the installation.
func TestSweepReclaimsLongStoppedDeploys(t *testing.T) {
	lister := &fakeLister{reclaimed: 3}
	cleaner := &fakeCleaner{}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if lister.reclaimCalls != 1 {
		t.Fatalf("the reclaim sweep ran %d times, want once per tick", lister.reclaimCalls)
	}
}

// Same independence rule as the other two: a failure in one half must not
// take the others with it.
func TestReclaimFailureDoesNotStopTheOtherSweeps(t *testing.T) {
	lister := &fakeLister{
		reclaimErr: errors.New("database is locked"),
		expired:    []billing.ExpiredDeploy{{ID: "deploy-1", ContainerID: "container-1"}},
		images:     []billing.ImageCleanup{{ID: "deploy-9", ImageRef: "snaphost/proj-abc:9"}},
	}
	cleaner := &fakeCleaner{}

	newWatchdog(lister, cleaner).sweep(context.Background())

	if len(cleaner.stopped) != 1 {
		t.Errorf("expiry was skipped: %v", cleaner.stopped)
	}
	if len(cleaner.removed) != 1 {
		t.Errorf("image cleanup was skipped: %v", cleaner.removed)
	}
}
