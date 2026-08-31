package saga

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

// A built image has to be nameable from the row the image sweep reads, and it
// was not.
//
// deploys.image_ref used to be written only by SetRunning, at the very end of a
// successful deploy. The build wrote its output to deploy_sagas.image_ref, so
// anything that went wrong between the build finishing and the container
// answering left an image on the host with a NULL in the only column the sweep
// looks at. The eager removal on the probe-failure path covered one route out;
// a container that could not be created, a compensating saga, and an eager
// removal that itself failed were not covered at all, and those images could
// never be reclaimed for the life of the installation.

func seedDeployAndSaga(t *testing.T, handle *sql.DB, status string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	owner := uuid.New()
	if _, err := handle.Exec(
		`INSERT INTO deploys (id, user_id, source_type, repo_url, status)
		 VALUES (?, ?, 'git_public', 'https://example.invalid/repo', ?)`,
		id.String(), owner.String(), status); err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	if _, err := handle.Exec(
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step) VALUES (?, ?, 'building')`,
		id.String(), owner.String()); err != nil {
		t.Fatalf("seed saga: %v", err)
	}
	return id
}

func TestMarkImageBuiltRecordsTheImageOnTheDeploy(t *testing.T) {
	repo, handle := rewindTestRepo(t)
	ctx := context.Background()

	id := seedDeployAndSaga(t, handle, "building")

	if err := repo.MarkImageBuilt(ctx, id, "snaphost/proj-abc:1", "abc1234", 8080); err != nil {
		t.Fatalf("MarkImageBuilt: %v", err)
	}

	var imageRef, commitSHA *string
	var deletedAt *string
	if err := handle.QueryRow(
		`SELECT image_ref, commit_sha, image_deleted_at FROM deploys WHERE id = ?`, id.String(),
	).Scan(&imageRef, &commitSHA, &deletedAt); err != nil {
		t.Fatalf("read deploy: %v", err)
	}
	if imageRef == nil || *imageRef != "snaphost/proj-abc:1" {
		t.Fatalf("deploys.image_ref = %v; the sweep cannot see an image it cannot name", imageRef)
	}
	if commitSHA == nil || *commitSHA != "abc1234" {
		t.Errorf("deploys.commit_sha = %v, want the build's", commitSHA)
	}
	if deletedAt != nil {
		t.Errorf("image_deleted_at = %q on a freshly built image", *deletedAt)
	}

	// And the saga still gets its own copy, which is what the resume path and
	// the restart port lookup read.
	var sagaRef *string
	var appPort *int64
	if err := handle.QueryRow(
		`SELECT image_ref, app_port FROM deploy_sagas WHERE deploy_id = ?`, id.String(),
	).Scan(&sagaRef, &appPort); err != nil {
		t.Fatalf("read saga: %v", err)
	}
	if sagaRef == nil || *sagaRef != "snaphost/proj-abc:1" {
		t.Errorf("deploy_sagas.image_ref = %v", sagaRef)
	}
	if appPort == nil || *appPort != 8080 {
		t.Errorf("app_port = %v, want the detected 8080", appPort)
	}
}

// The case the gap actually produced: a build succeeds, the deploy never
// reaches running, and the image has to still be collectable.
func TestABuiltImageIsSweepableEvenWhenTheDeployNeverRan(t *testing.T) {
	repo, handle := rewindTestRepo(t)
	ctx := context.Background()

	id := seedDeployAndSaga(t, handle, "building")
	if err := repo.MarkImageBuilt(ctx, id, "snaphost/proj-abc:1", "", 0); err != nil {
		t.Fatalf("MarkImageBuilt: %v", err)
	}
	// The container could not be created; the saga compensates and the deploy
	// is marked failed. Nothing else ever writes deploys.image_ref.
	if _, err := handle.Exec(`UPDATE deploys SET status = 'failed' WHERE id = ?`, id.String()); err != nil {
		t.Fatalf("fail the deploy: %v", err)
	}

	var queued int
	if err := handle.QueryRow(`
		SELECT count(*) FROM deploys
		WHERE id = ? AND status IN ('failed', 'deleted')
		  AND image_ref IS NOT NULL AND image_ref <> ''
		  AND image_deleted_at IS NULL`, id.String()).Scan(&queued); err != nil {
		t.Fatalf("run the sweep query: %v", err)
	}
	if queued != 1 {
		t.Fatal("a built image whose deploy never started is invisible to the image sweep")
	}
}

// An empty commit SHA must not blank one the deploy already carries — archive
// builds have no commit, and the deploy row is the only place it lives.
func TestMarkImageBuiltKeepsAnExistingCommitSHA(t *testing.T) {
	repo, handle := rewindTestRepo(t)
	ctx := context.Background()

	id := seedDeployAndSaga(t, handle, "building")
	if _, err := handle.Exec(
		`UPDATE deploys SET commit_sha = 'deadbee' WHERE id = ?`, id.String()); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	if err := repo.MarkImageBuilt(ctx, id, "snaphost/proj-abc:1", "", 0); err != nil {
		t.Fatalf("MarkImageBuilt: %v", err)
	}

	var commitSHA *string
	if err := handle.QueryRow(
		`SELECT commit_sha FROM deploys WHERE id = ?`, id.String()).Scan(&commitSHA); err != nil {
		t.Fatalf("read deploy: %v", err)
	}
	if commitSHA == nil || *commitSHA != "deadbee" {
		t.Errorf("commit_sha = %v, want the existing one kept", commitSHA)
	}
}
