package saga

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	controldb "snaphost/internal/control/db"
)

func rewindTestRepo(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()

	handle, err := controldb.Open(context.Background(), filepath.Join(t.TempDir(), "saga.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := controldb.RunMigrations(handle, zap.NewNop()); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	return NewRepository(handle), handle
}

func seedSaga(t *testing.T, handle *sql.DB, step Step, imageRef string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	var ref any
	if imageRef != "" {
		ref = imageRef
	}
	_, err := handle.Exec(
		`INSERT INTO deploy_sagas (deploy_id, user_id, current_step, image_ref) VALUES (?, ?, ?, ?)`,
		id.String(), uuid.NewString(), string(step), ref,
	)
	if err != nil {
		t.Fatalf("seed saga: %v", err)
	}
	return id
}

func stepOf(t *testing.T, handle *sql.DB, id uuid.UUID) Step {
	t.Helper()

	var step string
	if err := handle.QueryRow(
		`SELECT current_step FROM deploy_sagas WHERE deploy_id = ?`, id.String(),
	).Scan(&step); err != nil {
		t.Fatalf("read step: %v", err)
	}
	return Step(step)
}

// A restart during a build leaves a saga waiting on a build nobody is running.
// Rewinding it to the step that enqueues one is what replaces the Redis
// stream's un-acked redelivery.
func TestRewindMovesInterruptedBuildsBackToPending(t *testing.T) {
	repo, handle := rewindTestRepo(t)

	interrupted := seedSaga(t, handle, StepBuilding, "")

	moved, err := repo.RewindInterruptedBuilds(context.Background())
	if err != nil {
		t.Fatalf("RewindInterruptedBuilds: %v", err)
	}
	if moved != 1 {
		t.Fatalf("moved %d sagas, want 1", moved)
	}
	if got := stepOf(t, handle, interrupted); got != StepPending {
		t.Fatalf("step = %q, want %q", got, StepPending)
	}
}

// A saga whose build finished has its image on the row, and its wait step
// recognises that without any event. Rewinding it would build the same source
// twice — and on the archive path, against an upload that no longer exists.
func TestRewindLeavesSagasThatAlreadyHaveAnImage(t *testing.T) {
	repo, handle := rewindTestRepo(t)

	built := seedSaga(t, handle, StepBuilding, "registry/app:abc123")

	moved, err := repo.RewindInterruptedBuilds(context.Background())
	if err != nil {
		t.Fatalf("RewindInterruptedBuilds: %v", err)
	}
	if moved != 0 {
		t.Fatalf("moved %d sagas, want 0", moved)
	}
	if got := stepOf(t, handle, built); got != StepBuilding {
		t.Fatalf("step = %q, want it untouched at %q", got, StepBuilding)
	}
}

// Only the building step is rewound. Every other step is either past the build
// or already terminal, and moving one back would redo work that succeeded.
func TestRewindTouchesNoOtherStep(t *testing.T) {
	repo, handle := rewindTestRepo(t)

	others := map[Step]uuid.UUID{}
	for _, step := range []Step{StepPending, StepBuilt, StepProvisioning, StepRunning, StepFailed, StepCompensating} {
		others[step] = seedSaga(t, handle, step, "")
	}

	if _, err := repo.RewindInterruptedBuilds(context.Background()); err != nil {
		t.Fatalf("RewindInterruptedBuilds: %v", err)
	}
	for step, id := range others {
		if got := stepOf(t, handle, id); got != step {
			t.Errorf("saga at %q moved to %q", step, got)
		}
	}
}

// It runs at every start, so running it twice must be the same as running it
// once.
func TestRewindIsIdempotent(t *testing.T) {
	repo, handle := rewindTestRepo(t)
	id := seedSaga(t, handle, StepBuilding, "")
	ctx := context.Background()

	if _, err := repo.RewindInterruptedBuilds(ctx); err != nil {
		t.Fatalf("first rewind: %v", err)
	}
	moved, err := repo.RewindInterruptedBuilds(ctx)
	if err != nil {
		t.Fatalf("second rewind: %v", err)
	}
	if moved != 0 {
		t.Fatalf("the second rewind moved %d sagas", moved)
	}
	if got := stepOf(t, handle, id); got != StepPending {
		t.Fatalf("step = %q after two rewinds", got)
	}
}
