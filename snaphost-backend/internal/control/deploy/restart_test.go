package deploy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

// Starting a stopped deploy re-runs an image that is already on disk. The
// claim, the port and the refusals are all things a fake repository cannot
// check, so these run against a real migrated database.

func newRestartRepo(t *testing.T) *Repository {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "restart.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return NewRepository(handle)
}

type restartSeed struct {
	deployID uuid.UUID
	userID   uuid.UUID
}

func seedStopped(t *testing.T, repo *Repository, status, imageRef string, appPort *int) restartSeed {
	t.Helper()
	ctx := context.Background()

	s := restartSeed{deployID: uuid.New(), userID: uuid.New()}
	if _, err := repo.db.ExecContext(ctx,
		`INSERT INTO deploys (id, user_id, source_type, repo_url, status, image_ref, container_id)
		 VALUES (?, ?, 'git_public', 'https://example.invalid/repo', ?, ?, '')`,
		s.deployID, s.userID, status, imageRef); err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx,
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step, app_port)
		 VALUES (?, ?, 'running', ?)`, s.deployID, s.userID, appPort); err != nil {
		t.Fatalf("seed saga: %v", err)
	}
	return s
}

func port(n int) *int { return &n }

// The port comes off the saga because that is where the build recorded what
// EXPOSE said. Falling back to the default for a deploy that listens on 8080
// means probing a port nothing answers on and tearing the container straight
// back down — a restart that looks like a broken application.
func TestBeginRestartUsesThePortTheBuildDetected(t *testing.T) {
	repo := newRestartRepo(t)
	s := seedStopped(t, repo, "stopped", "snaphost/proj-abc:1", port(8080))

	target, err := repo.BeginRestart(context.Background(), s.deployID, 3000)
	if err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}
	if target.Port != 8080 {
		t.Errorf("Port = %d, want the saga's 8080 rather than the default", target.Port)
	}
	if target.ImageRef != "snaphost/proj-abc:1" {
		t.Errorf("ImageRef = %q", target.ImageRef)
	}
	if target.UserID != s.userID {
		t.Errorf("UserID = %v, want %v", target.UserID, s.userID)
	}
}

func TestBeginRestartFallsBackToTheDefaultPort(t *testing.T) {
	repo := newRestartRepo(t)
	s := seedStopped(t, repo, "stopped", "snaphost/proj-abc:1", nil)

	target, err := repo.BeginRestart(context.Background(), s.deployID, 3000)
	if err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}
	if target.Port != 3000 {
		t.Errorf("Port = %d, want the configured default", target.Port)
	}
}

// The claim is a guarded UPDATE precisely so a double click cannot start two
// containers for a row that records one. The second caller must lose.
func TestBeginRestartClaimsTheDeployExactlyOnce(t *testing.T) {
	repo := newRestartRepo(t)
	s := seedStopped(t, repo, "stopped", "snaphost/proj-abc:1", port(3000))
	ctx := context.Background()

	if _, err := repo.BeginRestart(ctx, s.deployID, 3000); err != nil {
		t.Fatalf("first BeginRestart: %v", err)
	}
	if _, err := repo.BeginRestart(ctx, s.deployID, 3000); err != ErrNotStopped {
		t.Fatalf("second BeginRestart = %v, want ErrNotStopped", err)
	}

	var status string
	if err := repo.db.QueryRowContext(ctx,
		`SELECT status FROM deploys WHERE id = ?`, s.deployID.String()).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "provisioning" {
		t.Errorf("status = %q, want provisioning while the start is in flight", status)
	}
}

// An image the sweep reclaimed cannot be re-run. Refusing is the honest
// answer: rebuilding is a different operation with a different cost, and the
// operator should be the one choosing it.
func TestBeginRestartRefusesAReclaimedImage(t *testing.T) {
	repo := newRestartRepo(t)
	s := seedStopped(t, repo, "stopped", "snaphost/proj-abc:1", port(3000))
	ctx := context.Background()

	if err := repo.MarkImageDeleted(ctx, s.deployID); err != nil {
		t.Fatalf("MarkImageDeleted: %v", err)
	}
	if _, err := repo.BeginRestart(ctx, s.deployID, 3000); err != ErrNotRestartable {
		t.Fatalf("BeginRestart = %v, want ErrNotRestartable", err)
	}

	// And the refusal must not have claimed it.
	var status string
	if err := repo.db.QueryRowContext(ctx,
		`SELECT status FROM deploys WHERE id = ?`, s.deployID.String()).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "stopped" {
		t.Errorf("status = %q after a refused restart, want stopped", status)
	}
}

func TestBeginRestartRefusesADeployWithNoImageAtAll(t *testing.T) {
	repo := newRestartRepo(t)
	s := seedStopped(t, repo, "stopped", "", port(3000))

	if _, err := repo.BeginRestart(context.Background(), s.deployID, 3000); err != ErrNotRestartable {
		t.Fatalf("BeginRestart = %v, want ErrNotRestartable", err)
	}
}

func TestBeginRestartOnlyAppliesToStoppedDeploys(t *testing.T) {
	for _, status := range []string{"running", "failed", "deleted", "building", "pending", "provisioning"} {
		t.Run(status, func(t *testing.T) {
			repo := newRestartRepo(t)
			s := seedStopped(t, repo, status, "snaphost/proj-abc:1", port(3000))

			if _, err := repo.BeginRestart(context.Background(), s.deployID, 3000); err != ErrNotStopped {
				t.Fatalf("BeginRestart on %q = %v, want ErrNotStopped", status, err)
			}
		})
	}
}

func TestBeginRestartRejectsAnUnknownDeploy(t *testing.T) {
	repo := newRestartRepo(t)

	if _, err := repo.BeginRestart(context.Background(), uuid.New(), 3000); err != ErrDeployNotFound {
		t.Fatalf("BeginRestart = %v, want ErrDeployNotFound", err)
	}
}

// Without this the deploy is stuck: nothing sweeps 'provisioning', so the
// button disappears and the row claims to be starting for the rest of the
// installation's life.
func TestAbandonRestartReleasesTheClaim(t *testing.T) {
	repo := newRestartRepo(t)
	s := seedStopped(t, repo, "stopped", "snaphost/proj-abc:1", port(3000))
	ctx := context.Background()

	if _, err := repo.BeginRestart(ctx, s.deployID, 3000); err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}
	if err := repo.AbandonRestart(ctx, s.deployID, "the container never answered on PORT"); err != nil {
		t.Fatalf("AbandonRestart: %v", err)
	}

	var status string
	var reason *string
	if err := repo.db.QueryRowContext(ctx,
		`SELECT status, failure_reason FROM deploys WHERE id = ?`, s.deployID.String()).Scan(&status, &reason); err != nil {
		t.Fatalf("read deploy: %v", err)
	}
	if status != "stopped" {
		t.Fatalf("status = %q, want stopped so the button comes back", status)
	}
	if reason == nil || *reason != "the container never answered on PORT" {
		t.Errorf("failure_reason = %v, want the runtime's reason kept for the operator", reason)
	}

	// And the deploy is claimable again.
	if _, err := repo.BeginRestart(ctx, s.deployID, 3000); err != nil {
		t.Fatalf("BeginRestart after abandon: %v", err)
	}
}

// A running deploy must not be knocked back to stopped by a stray abandon —
// the guard is on the status, not just the id.
func TestAbandonRestartOnlyTouchesAClaimedDeploy(t *testing.T) {
	repo := newRestartRepo(t)
	s := seedStopped(t, repo, "running", "snaphost/proj-abc:1", port(3000))
	ctx := context.Background()

	if err := repo.AbandonRestart(ctx, s.deployID, "stale"); err != nil {
		t.Fatalf("AbandonRestart: %v", err)
	}
	var status string
	if err := repo.db.QueryRowContext(ctx,
		`SELECT status FROM deploys WHERE id = ?`, s.deployID.String()).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "running" {
		t.Fatalf("a running deploy was moved to %q by an abandon it had nothing to do with", status)
	}
}
