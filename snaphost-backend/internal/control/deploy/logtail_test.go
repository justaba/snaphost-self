package deploy

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

// fakeArchiver stands in for the log bus and records what it was asked for, so
// the tests can assert not only what was stored but that a successful deploy
// never asks at all.
type fakeArchiver struct {
	blob  []byte
	asked []string
}

func (f *fakeArchiver) Archive(deployID string) []byte {
	f.asked = append(f.asked, deployID)
	return f.blob
}

func tailTestPool(t *testing.T) *sql.DB {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "deploy.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return handle
}

func seedDeploy(t *testing.T, r *Repository) uuid.UUID {
	t.Helper()

	id := uuid.New()
	err := r.Create(context.Background(), Deploy{
		ID: id, UserID: uuid.New(), RepoURL: "https://example.test/repo", Branch: "main", Status: "pending",
	})
	if err != nil {
		t.Fatalf("create deploy: %v", err)
	}
	return id
}

func TestFailingADeployArchivesItsLogTail(t *testing.T) {
	archiver := &fakeArchiver{blob: []byte(`[{"text":"build failed"}]`)}
	repo := NewRepository(tailTestPool(t), WithLogArchiver(archiver))
	ctx := context.Background()

	id := seedDeploy(t, repo)
	reason := "exit status 1"
	if err := repo.UpdateStatus(ctx, id, StatusFailed, &reason); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	stored, err := repo.LogTail(ctx, id)
	if err != nil {
		t.Fatalf("LogTail: %v", err)
	}
	if string(stored) != string(archiver.blob) {
		t.Fatalf("stored tail = %q, want %q", stored, archiver.blob)
	}
}

// Only failures are archived. A successful build's output is read while it
// scrolls past; storing it would put a blob on every row for nobody.
func TestNonFailureStatusesDoNotArchive(t *testing.T) {
	archiver := &fakeArchiver{blob: []byte(`[{"text":"noise"}]`)}
	repo := NewRepository(tailTestPool(t), WithLogArchiver(archiver))
	ctx := context.Background()

	id := seedDeploy(t, repo)
	for _, status := range []string{"building", "provisioning", "running", "stopped"} {
		if err := repo.UpdateStatus(ctx, id, status, nil); err != nil {
			t.Fatalf("UpdateStatus(%s): %v", status, err)
		}
	}

	if len(archiver.asked) != 0 {
		t.Fatalf("the archiver was consulted for %v; only 'failed' should reach it", archiver.asked)
	}
	stored, err := repo.LogTail(ctx, id)
	if err != nil {
		t.Fatalf("LogTail: %v", err)
	}
	if stored != nil {
		t.Fatalf("log_tail = %q on a deploy that never failed", stored)
	}
}

// The same failure may be written twice — the runtime marks a deploy failed and
// so does the saga behind it. The second pass must not corrupt what the first
// stored.
func TestArchivingIsIdempotent(t *testing.T) {
	archiver := &fakeArchiver{blob: []byte(`[{"text":"build failed"}]`)}
	repo := NewRepository(tailTestPool(t), WithLogArchiver(archiver))
	ctx := context.Background()

	id := seedDeploy(t, repo)
	reason := "exit status 1"
	for i := 0; i < 3; i++ {
		if err := repo.UpdateStatus(ctx, id, StatusFailed, &reason); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
	}

	stored, err := repo.LogTail(ctx, id)
	if err != nil {
		t.Fatalf("LogTail: %v", err)
	}
	if string(stored) != string(archiver.blob) {
		t.Fatalf("stored tail = %q after three writes", stored)
	}
}

// An empty tail must not overwrite a stored one. A deploy whose logs were
// already evicted from memory can be re-marked failed, and blanking the column
// would lose the only copy.
func TestAnEmptyTailDoesNotOverwriteAStoredOne(t *testing.T) {
	archiver := &fakeArchiver{blob: []byte(`[{"text":"the real failure"}]`)}
	repo := NewRepository(tailTestPool(t), WithLogArchiver(archiver))
	ctx := context.Background()

	id := seedDeploy(t, repo)
	reason := "exit status 1"
	if err := repo.UpdateStatus(ctx, id, StatusFailed, &reason); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	archiver.blob = nil
	if err := repo.UpdateStatus(ctx, id, StatusFailed, &reason); err != nil {
		t.Fatalf("second UpdateStatus: %v", err)
	}

	stored, err := repo.LogTail(ctx, id)
	if err != nil {
		t.Fatalf("LogTail: %v", err)
	}
	if string(stored) != `[{"text":"the real failure"}]` {
		t.Fatalf("stored tail = %q; an empty archive erased it", stored)
	}
}

// A repository built without the option must still work. The runtime and the
// builder construct one before anything has a bus to give them in some tests.
func TestUpdateStatusWithoutAnArchiver(t *testing.T) {
	repo := NewRepository(tailTestPool(t))
	ctx := context.Background()

	id := seedDeploy(t, repo)
	reason := "exit status 1"
	if err := repo.UpdateStatus(ctx, id, StatusFailed, &reason); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if tail, err := repo.LogTail(ctx, id); err != nil || tail != nil {
		t.Fatalf("LogTail = %q, %v; want nil, nil", tail, err)
	}
}

func TestLogTailOfAnUnknownDeployIsNotAnError(t *testing.T) {
	repo := NewRepository(tailTestPool(t))

	tail, err := repo.LogTail(context.Background(), uuid.New())
	if err != nil || tail != nil {
		t.Fatalf("LogTail = %q, %v; want nil, nil", tail, err)
	}
}
