package deploy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

// The image sweep is the same shape as the reclaim sweep, and the reclaim
// sweep never ran once: its statement had four placeholders and two arguments,
// which every test in this package missed because they matched the SQL as a
// string. So these execute against a real migrated database instead.
//
// What they have to catch beyond arity is the selection rule itself. A sweep
// that picks the wrong rows here does not fail — it deletes a live site's
// image, or it deletes nothing and the disk fills, and both are silent.
func newImageGCRepo(t *testing.T) *Repository {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "imagegc.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })

	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return NewRepository(handle)
}

func seedDeployWithImage(t *testing.T, repo *Repository, status, imageRef string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := repo.db.ExecContext(context.Background(),
		`INSERT INTO deploys (id, user_id, source_type, repo_url, status, image_ref)
		 VALUES (?, ?, 'git_public', 'https://example.invalid/repo', ?, ?)`,
		id, uuid.New(), status, imageRef,
	)
	if err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	return id
}

func pendingIDs(t *testing.T, repo *Repository) map[uuid.UUID]string {
	t.Helper()

	rows, err := repo.FindImagesPendingCleanup(context.Background(), 50)
	if err != nil {
		t.Fatalf("FindImagesPendingCleanup: %v", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.ImageRef
	}
	return out
}

// The sweep takes the images nothing can ever start again and leaves the rest.
//
// 'stopped' is the interesting one and it is deliberately spared: its image is
// what makes POST /deploys/:id/start cheap, so reclaiming it would turn every
// stop into a rebuild. A running or provisioning deploy is a live site whose
// container came out of that image, which is the case that must never be swept
// at all.
func TestImageCleanupSelectsOnlyDeadDeploys(t *testing.T) {
	repo := newImageGCRepo(t)

	dead := map[string]uuid.UUID{}
	for _, status := range []string{"failed", "deleted"} {
		dead[status] = seedDeployWithImage(t, repo, status, "snaphost/proj-abc:"+status)
	}
	spared := map[string]uuid.UUID{}
	for _, status := range []string{"pending", "building", "provisioning", "running", "stopped"} {
		spared[status] = seedDeployWithImage(t, repo, status, "snaphost/proj-abc:"+status)
	}

	got := pendingIDs(t, repo)

	for status, id := range dead {
		if _, ok := got[id]; !ok {
			t.Errorf("deploy in status %q was not queued for image cleanup", status)
		}
	}
	for status, id := range spared {
		if _, ok := got[id]; ok {
			t.Errorf("deploy in status %q was queued for image cleanup; %s",
				status, "a stopped deploy needs its image to start again, and a live one is running from it")
		}
	}
}

// The exact case the restart button depends on, stated on its own so that
// re-adding 'stopped' to the sweep fails here with a message naming why.
func TestStoppedDeployKeepsItsImageForRestart(t *testing.T) {
	repo := newImageGCRepo(t)

	id := seedDeployWithImage(t, repo, "stopped", "snaphost/proj-abc:1")
	if _, ok := pendingIDs(t, repo)[id]; ok {
		t.Fatal("a stopped deploy was queued for image cleanup; starting it again would need a full rebuild")
	}

	// Deleting it is what releases the disk.
	if err := repo.MarkDeleted(context.Background(), id); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}
	if _, ok := pendingIDs(t, repo)[id]; !ok {
		t.Fatal("a deleted deploy was not queued; its image would never be reclaimed")
	}
}

// image_ref survives cleanup for diagnostics, so the marker is what makes the
// sweep idempotent. Without it every tick would re-issue a Docker call for
// every deploy this platform has ever built.
func TestMarkImageDeletedRemovesRowFromTheQueue(t *testing.T) {
	repo := newImageGCRepo(t)
	ctx := context.Background()

	id := seedDeployWithImage(t, repo, "failed", "snaphost/proj-abc:1")

	if _, ok := pendingIDs(t, repo)[id]; !ok {
		t.Fatal("a failed deploy holding an image is not queued")
	}
	if err := repo.MarkImageDeleted(ctx, id); err != nil {
		t.Fatalf("MarkImageDeleted: %v", err)
	}
	if _, ok := pendingIDs(t, repo)[id]; ok {
		t.Fatal("a deploy still queued after its image was marked deleted")
	}

	// The second call is what a retry after a crash looks like.
	if err := repo.MarkImageDeleted(ctx, id); err != nil {
		t.Fatalf("MarkImageDeleted is not idempotent: %v", err)
	}

	var imageRef string
	var deletedAt *string
	err := repo.db.QueryRowContext(ctx,
		`SELECT image_ref, image_deleted_at FROM deploys WHERE id = ?`, id.String(),
	).Scan(&imageRef, &deletedAt)
	if err != nil {
		t.Fatalf("read back deploy: %v", err)
	}
	if imageRef != "snaphost/proj-abc:1" {
		t.Errorf("image_ref = %q, want it kept for diagnostics after cleanup", imageRef)
	}
	if deletedAt == nil {
		t.Fatal("image_deleted_at was not written")
	}
	// COALESCE keeps the first marker: a retry must not restamp the row, or
	// "when did this disk space come back" stops being answerable.
	if err := repo.MarkImageDeleted(ctx, id); err != nil {
		t.Fatalf("MarkImageDeleted: %v", err)
	}
	var second *string
	if err := repo.db.QueryRowContext(ctx,
		`SELECT image_deleted_at FROM deploys WHERE id = ?`, id.String()).Scan(&second); err != nil {
		t.Fatalf("read back marker: %v", err)
	}
	if second == nil || *second != *deletedAt {
		t.Errorf("marker moved on retry: %v then %v", deletedAt, second)
	}
}

// A deploy that failed before any image existed has nothing to sweep. Queuing
// it would put a row in front of the watchdog that can never be cleared.
func TestImageCleanupSkipsDeploysWithoutAnImage(t *testing.T) {
	repo := newImageGCRepo(t)

	empty := seedDeployWithImage(t, repo, "failed", "")
	withImage := seedDeployWithImage(t, repo, "failed", "snaphost/proj-abc:1")

	got := pendingIDs(t, repo)
	if _, ok := got[empty]; ok {
		t.Error("a deploy with an empty image_ref was queued for cleanup")
	}
	if _, ok := got[withImage]; !ok {
		t.Error("a deploy with an image_ref was not queued for cleanup")
	}
}

func TestMarkImageDeletedRejectsAnUnknownDeploy(t *testing.T) {
	repo := newImageGCRepo(t)

	if err := repo.MarkImageDeleted(context.Background(), uuid.New()); err == nil {
		t.Fatal("marking an unknown deploy succeeded; the runtime would record cleanup it never did")
	}
}

// SetRunning clears the marker. A row is reused by nothing today, but the
// column and the status would silently disagree if a future path did reuse it:
// a running deploy whose image is recorded as already deleted is a site the
// sweep would skip and the operator could not explain.
func TestSetRunningClearsTheCleanupMarker(t *testing.T) {
	repo := newImageGCRepo(t)
	ctx := context.Background()

	id := seedDeployWithImage(t, repo, "failed", "snaphost/proj-abc:1")
	if err := repo.MarkImageDeleted(ctx, id); err != nil {
		t.Fatalf("MarkImageDeleted: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx,
		`UPDATE deploys SET status = 'building' WHERE id = ?`, id.String()); err != nil {
		t.Fatalf("reset status: %v", err)
	}
	// SetRunning writes both durable views in one transaction, so the saga row
	// has to exist or it refuses.
	if _, err := repo.db.ExecContext(ctx,
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step) VALUES (?, ?, 'built')`,
		id.String(), uuid.New().String()); err != nil {
		t.Fatalf("seed saga: %v", err)
	}

	if err := repo.SetRunning(ctx, id, "snaphost/proj-abc:2", "https://x.invalid", "sub", "container-1",
		time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("SetRunning: %v", err)
	}

	var deletedAt *string
	if err := repo.db.QueryRowContext(ctx,
		`SELECT image_deleted_at FROM deploys WHERE id = ?`, id.String()).Scan(&deletedAt); err != nil {
		t.Fatalf("read back marker: %v", err)
	}
	if deletedAt != nil {
		t.Fatalf("image_deleted_at survived SetRunning: %q", *deletedAt)
	}
}

// The leak this closes had no ceiling.
//
// The watchdog's TTL and retention sweeps both end at 'stopped', and the image
// sweep skips 'stopped' so the deploy stays startable. MarkDeleted has exactly
// one caller — the delete button — so nothing automatic ever moved a deploy
// out of 'stopped'. Every preview that ever expired kept a container image on
// the host forever, which is the disk-growth problem image GC exists to solve.
func newGraceRepo(t *testing.T, graceHours int) *Repository {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "grace.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return NewRepository(handle, WithGCPolicy(GCPolicy{StoppedImageGraceHours: graceHours}))
}

func seedStoppedAt(t *testing.T, repo *Repository, stoppedAt time.Time) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if _, err := repo.db.ExecContext(context.Background(),
		`INSERT INTO deploys (id, user_id, source_type, repo_url, status, image_ref, stopped_at)
		 VALUES (?, ?, 'git_public', 'https://example.invalid/repo', 'stopped', ?, ?)`,
		id, uuid.New(), "snaphost/proj-abc:"+id.String()[:8], controldb.FormatTime(stoppedAt),
	); err != nil {
		t.Fatalf("seed stopped deploy: %v", err)
	}
	return id
}

func statusOf(t *testing.T, repo *Repository, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := repo.db.QueryRowContext(context.Background(),
		`SELECT status FROM deploys WHERE id = ?`, id.String()).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}

func TestReclaimMovesLongStoppedDeploysToDeleted(t *testing.T) {
	repo := newGraceRepo(t, 168)
	ctx := context.Background()

	old := seedStoppedAt(t, repo, time.Now().Add(-8*24*time.Hour))
	recent := seedStoppedAt(t, repo, time.Now().Add(-time.Hour))

	n, err := repo.ReclaimStoppedDeploys(ctx, 50)
	if err != nil {
		t.Fatalf("ReclaimStoppedDeploys: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed %d deploys, want only the one past its grace", n)
	}
	if got := statusOf(t, repo, old); got != "deleted" {
		t.Errorf("the long-stopped deploy is %q, want deleted so its image is queued", got)
	}
	if got := statusOf(t, repo, recent); got != "stopped" {
		t.Errorf("a recently stopped deploy became %q; it must stay startable", got)
	}

	// The point of the transition: the image sweep can now see it.
	if _, ok := pendingIDs(t, repo)[old]; !ok {
		t.Error("the reclaimed deploy is not queued for image cleanup")
	}
	if _, ok := pendingIDs(t, repo)[recent]; ok {
		t.Error("a still-startable deploy was queued for image cleanup")
	}
}

// Someone's hostname points at it. Same rule the TTL sweep already follows.
func TestReclaimSparesAnAliasedDeploy(t *testing.T) {
	repo := newGraceRepo(t, 168)
	ctx := context.Background()

	id := seedStoppedAt(t, repo, time.Now().Add(-30*24*time.Hour))
	owner := uuid.New()
	projectID := uuid.New()
	if _, err := repo.db.ExecContext(ctx,
		`INSERT INTO users (id, email) VALUES (?, ?)`, owner.String(), "op@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx,
		`INSERT INTO projects (id, user_id, slug, source_key) VALUES (?, ?, 'demo', 'git:x#main')`,
		projectID.String(), owner.String()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx, `
		INSERT INTO custom_domains (id, user_id, project_id, target_deploy_id, domain, verification_token, status, verified_at)
		VALUES (?, ?, ?, ?, 'app.example.test', 'token', 'verified', ?)`,
		uuid.NewString(), owner.String(), projectID.String(), id.String(), controldb.Now()); err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	if _, err := repo.ReclaimStoppedDeploys(ctx, 50); err != nil {
		t.Fatalf("ReclaimStoppedDeploys: %v", err)
	}
	if got := statusOf(t, repo, id); got != "stopped" {
		t.Errorf("an alias-published deploy was reclaimed: status %q", got)
	}
}

// Zero disables the sweep. An operator who wants images kept indefinitely
// should get that rather than a surprise default.
func TestReclaimIsOffWhenTheGraceIsZero(t *testing.T) {
	repo := newGraceRepo(t, 0)

	id := seedStoppedAt(t, repo, time.Now().Add(-365*24*time.Hour))

	n, err := repo.ReclaimStoppedDeploys(context.Background(), 50)
	if err != nil {
		t.Fatalf("ReclaimStoppedDeploys: %v", err)
	}
	if n != 0 || statusOf(t, repo, id) != "stopped" {
		t.Errorf("the sweep ran with a zero grace: reclaimed %d", n)
	}
}

// A stopped deploy with no stopped_at is not reclaimed, and that is a
// deliberate trade. updated_at is rewritten by a trigger on every UPDATE, so
// using it as a fallback would reset the grace clock on any write and could
// keep a row alive indefinitely. Every path to stopped sets stopped_at.
func TestReclaimIgnoresAStoppedDeployWithNoStopTime(t *testing.T) {
	repo := newGraceRepo(t, 1)
	ctx := context.Background()

	id := seedStoppedAt(t, repo, time.Now().Add(-48*time.Hour))
	if _, err := repo.db.ExecContext(ctx,
		`UPDATE deploys SET stopped_at = NULL WHERE id = ?`, id.String()); err != nil {
		t.Fatalf("clear stopped_at: %v", err)
	}

	n, err := repo.ReclaimStoppedDeploys(ctx, 50)
	if err != nil {
		t.Fatalf("ReclaimStoppedDeploys: %v", err)
	}
	if n != 0 || statusOf(t, repo, id) != "stopped" {
		t.Errorf("a deploy with no stop time was reclaimed on a clock that resets: %d", n)
	}
}

// The grace has to run from the last stop, not the first.
//
// stopped_at is written with a COALESCE so it records the first stop and never
// moves — correct when nothing could restart a deploy, and wrong the moment
// something can. A deploy stopped in January, restarted, and stopped again in
// June would be measured from January and have its image taken on the next
// tick, which is the opposite of what the grace is for. SetRunning clears it.
func TestRestartResetsTheReclaimClock(t *testing.T) {
	repo := newGraceRepo(t, 168)
	ctx := context.Background()

	// Stopped long ago: past the grace, so it would be reclaimed right now.
	id := seedStoppedAt(t, repo, time.Now().Add(-30*24*time.Hour))
	if _, err := repo.db.ExecContext(ctx,
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step) VALUES (?, ?, 'built')`,
		id.String(), uuid.NewString()); err != nil {
		t.Fatalf("seed saga: %v", err)
	}

	// It is started again, through the real path: the claim moves it to
	// provisioning, then the runtime reports it running.
	if _, err := repo.BeginRestart(ctx, id, 3000); err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}
	if err := repo.SetRunning(ctx, id, "snaphost/proj-abc:1", "https://x.invalid", "sub",
		"container-1", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("SetRunning: %v", err)
	}
	var stoppedAt *string
	if err := repo.db.QueryRowContext(ctx,
		`SELECT stopped_at FROM deploys WHERE id = ?`, id.String()).Scan(&stoppedAt); err != nil {
		t.Fatalf("read stopped_at: %v", err)
	}
	if stoppedAt != nil {
		t.Fatalf("stopped_at = %q on a running deploy; the reclaim clock never resets", *stoppedAt)
	}

	// Stopped again, just now. The grace starts over.
	if err := repo.UpdateStatus(ctx, id, "stopped", nil); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	n, err := repo.ReclaimStoppedDeploys(ctx, 50)
	if err != nil {
		t.Fatalf("ReclaimStoppedDeploys: %v", err)
	}
	if n != 0 || statusOf(t, repo, id) != "stopped" {
		t.Fatalf("a just-stopped deploy was reclaimed on its previous stop time: %d", n)
	}
}

// The moment an image exists on the host, the database says so. Everything
// after the build can reject the deploy, and several of those paths used to
// leave an artifact the sweep could not name.
func TestSetImageRefMakesAnImageSweepableBeforeTheScan(t *testing.T) {
	repo := newImageGCRepo(t)
	ctx := context.Background()

	id := seedDeployWithImage(t, repo, "building", "")
	if err := repo.SetImageRef(ctx, id, "snaphost/proj-abc:1"); err != nil {
		t.Fatalf("SetImageRef: %v", err)
	}

	// The scan rejects it and the gate's best-effort removal fails.
	if err := repo.UpdateStatus(ctx, id, "failed", nil); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, ok := pendingIDs(t, repo)[id]
	if !ok {
		t.Fatal("an image rejected by the scan is invisible to the sweep")
	}
	if got != "snaphost/proj-abc:1" {
		t.Errorf("queued image_ref = %q", got)
	}
}

// A rebuild into a row whose image was previously reclaimed must not inherit
// the old marker, or the new artifact is hidden from the sweep for good.
func TestSetImageRefClearsAStaleCleanupMarker(t *testing.T) {
	repo := newImageGCRepo(t)
	ctx := context.Background()

	id := seedDeployWithImage(t, repo, "failed", "snaphost/proj-abc:old")
	if err := repo.MarkImageDeleted(ctx, id); err != nil {
		t.Fatalf("MarkImageDeleted: %v", err)
	}
	if err := repo.SetImageRef(ctx, id, "snaphost/proj-abc:new"); err != nil {
		t.Fatalf("SetImageRef: %v", err)
	}

	got, ok := pendingIDs(t, repo)[id]
	if !ok || got != "snaphost/proj-abc:new" {
		t.Fatalf("the rebuilt image is not queued: %q ok=%v", got, ok)
	}
}

func TestSetImageRefRejectsAnUnknownDeploy(t *testing.T) {
	repo := newImageGCRepo(t)

	if err := repo.SetImageRef(context.Background(), uuid.New(), "snaphost/proj-abc:1"); err == nil {
		t.Fatal("recording an image against a deploy that does not exist succeeded")
	}
}
